# LinkRaft 設計メモ

チーム共有リンクボードサービス。リンクを保存・共有・投票・コメントできる Web アプリケーション。
最大の特徴として、ストレージに RDB や既製の KVS ではなく
**自作の分散キーバリューストア（Raft 合意アルゴリズム、3 ノード構成）** を使う。

> このファイルは実装済みコードを元に再作成した設計メモ（元の claude.md は未コミットで消失）。

## 全体アーキテクチャ

```
ブラウザ (React SPA)
   │ HTTP (/api/*、静的ファイル)
   ▼
API サーバー (Echo, :8080)  ← JWT 認証、バリデーション、OGP 取得
   │ gRPC (Set/Get/Delete/Incr/Keys)
   ▼
分散 KVS クラスタ（3 ノード, :9000〜:9002）
   ├─ Raft: リーダー選出・ログ複製（gRPC トランスポート）
   └─ 各ノード: インメモリストア + WAL / Raft ログをディスク永続化
```

- 書き込み（Set / Delete / Incr）はリーダーで Raft 合意（過半数複製）後に適用
- フォロワーへの書き込みはリーダーのアドレスヒントを返し、API 側クライアントが自動追従
- ノード再起動時は WAL と Raft ログから復元し、リーダーからキャッチアップ

## データ設計（KVS のキー命名）

JOIN がないため、表示用フィールド（user_name など）は書き込み時に非正規化して埋める。
一覧系は「ID のリストを持つインデックスキー」を手動でメンテナンスする。

| キー | 値 | 用途 |
| --- | --- | --- |
| `user:{id}` | User JSON | ユーザー本体 |
| `email:{email}` | user_id | メールアドレス→ID の逆引き（重複登録防止） |
| `link:{id}` | Link JSON | リンク本体（vote_count / comment_count を内包） |
| `comment:{id}` | Comment JSON | コメント（parent_id で 1 階層返信） |
| `vote:{link_id}:{user_id}` | Vote JSON | 投票（存在チェックで重複防止、トグル式） |
| `seq:user` / `seq:link` / `seq:comment` | int64 | Incr による ID 採番 |
| `index:links:recent` | [link_id] | 新着一覧（先頭に prepend） |
| `index:links:tag:{tag}` | [link_id] | タグ別一覧 |
| `index:links:user:{user_id}` | [link_id] | ユーザーの投稿一覧 |
| `index:comments:link:{link_id}` | [comment_id] | リンクのコメント一覧（末尾に append） |

- 人気順（popular）はインデックスを持たず、クエリ時に vote_count でソート
- 複合更新（リンク作成 + 各インデックス更新など）は API 側の Repo ロックで直列化

## 実装フェーズ

### フェーズ 1: バックエンド（完了）

- Raft（リーダー選出・ログ複製・永続化）と分散 KVS ノード（gRPC）
- Echo REST API（認証・リンク・投票・コメント・プロフィール・OGP・ヘルス）
- go test -race のユニット/ハンドラーテスト、Docker Compose 一発起動

### フェーズ 2: フロントエンド（完了）

- `web/` に Vite + React + TypeScript の SPA（追加ライブラリは react-router-dom のみ）
- ページ: 一覧（新着/人気タブ・タグ絞り込み・検索・ページネーション）、
  リンク詳細（コメント + 1 階層返信）、投稿（OGP プリフィル）、
  ログイン/サインアップ、ユーザープロフィール、クラスタ状態（2 秒ポーリング）
- JWT は localStorage に保持し、Context（AuthProvider）でログイン状態を共有
- 開発時: Vite dev server (:5173) が `/api` を API サーバー (:8080) にプロキシ
- 本番: `npm run build` の成果物 `web/dist` を API サーバーが配信
  （SPA フォールバック付き、`-web` フラグ / `WEB_DIR` で指定、Dockerfile.api に同梱）

### フェーズ 3: 発展

#### 3-1. KVS のスナップショット / ログコンパクション（完了）

- しきい値（`-snapshot-threshold`、デフォルト 1000 エントリ）を超えて適用が進むと、
  ステートマシン全体を `raft-snapshot.json` に永続化し、Raft ログと WAL を切り詰める
- 起動時はスナップショット → WAL 残分 → Raft ログの順で復元（各段階で適用済み
  インデックス以下のレコードは読み飛ばすためクラッシュ耐性がある）
- ログに残っていない範囲まで遅れたフォロワーには InstallSnapshot RPC で
  スナップショットを転送してから通常の複製に戻る（チャンク分割なしの簡易版）
- 実装: `raft/snapshot.go`（Snapshot / Snapshotter）、`raft/log.go`（CompactTo）、
  `kvs/store.go`（Snapshotter 実装）。Store が raft.Snapshotter を実装する

#### 3-2. pre-vote / ReadIndex / メンバーシップ変更（完了）

- **pre-vote**（論文 §9.6）: 選挙タイムアウト時、term を上げる前に
  `RequestVote(pre_vote=true)` で打診し、過半数が「投票する」と答えたときだけ
  本選挙に進む。受信側は term / votedFor / 選挙タイマーを一切変更せず、
  現リーダーのハートビートを受け取れている間は拒否する（leader stickiness）。
  これにより一方向断・復帰ノード・構成から除去されたノードが
  健全なリーダーを乱さない。実装: `raft.go`（startPreVoteLocked / handleRequestVote）
- **ReadIndex 線形化読み取り**（論文 §6.4）: `Node.ReadIndex(ctx)` が
  (1) 現 term の no-op コミットを待ち、(2) 空 AppendEntries で過半数の生存確認、
  (3) その時点の commitIndex の適用を待つ。KVS の Get / Keys がこれを通るので
  読み取りは線形化可能。lastApplied はステートマシン適用完了後に進める。
  実装: `raft/readindex.go`、`kvs/server.go`
- **メンバーシップ変更**（単一サーバー方式、論文 §4.1 の簡易版）: 一度に
  1 ノードずつ追加・削除。全メンバーの id -> addr マップを JSON にした
  EntryConfig エントリとして複製し、各ノードは追記時点で構成を切り替える。
  前の変更が未コミットの間は次を拒否。リーダー自身の削除は拒否（簡略化）。
  スナップショットにも構成を埋め込み、再起動時は snapshot → ログの順で復元。
  新ノードは `-join` フラグで起動（構成に加わるまで選挙を起こさない）し、
  Web UI（クラスタ状態ページ）か `POST /api/cluster/members` で追加する。
  実装: `raft/membership.go`、`kvs/server.go`、`api/handler/cluster.go`
- gRPC 再接続バックオフの上限を 2 秒に設定（デフォルトは最大 ~2 分で、
  落ちたノードの復帰検知が遅れるため）。実装: `raft.DialOptions`
- フロントエンド: クラスタ状態ページをカード表示に刷新（term / commit /
  applied / 複製進捗バー / メンバーシップ操作 UI）、投稿フォームに
  既存タグのサジェスト（`GET /api/tags`）

#### 残り候補（未着手）

- リーダーシップ移譲（TimeoutNow）とリーダー自身の削除対応
- InstallSnapshot のチャンク分割（大きなスナップショット対応）
- E2E テスト（Playwright）と CI

## 開発コマンド

```sh
make run-kvs-0 / run-kvs-1 / run-kvs-2 / run-api   # ローカル 4 プロセス起動
make web       # フロントエンドを web/dist にビルド
make web-dev   # Vite dev server (:5173)
make test      # go test -race ./...
docker compose up --build                          # 一発起動
```
