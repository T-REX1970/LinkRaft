# LinkRaft

チーム共有リンクボードサービス。リンクを保存・共有・投票できる Web アプリケーション。
ストレージには **自作の分散キーバリューストア（Raft 合意アルゴリズム、3 ノード構成）** を使用する。

全体設計・データ設計・実装フェーズは [claude.md](./claude.md) を参照。

## 構成

```
cmd/
├── api/    # Echo REST API サーバー（:8080、web/dist があれば SPA も配信）
└── kvs/    # 分散 KVS ノード（gRPC, :9000〜）
internal/
├── api/    # ルーティング・ハンドラー・JWT ミドルウェア
├── kvs/    # インメモリストア + WAL、gRPC サーバー/クライアント
├── raft/   # Raft（リーダー選出・ログ複製・gRPC トランスポート）
└── model/  # ドメインモデル（User / Link / Vote / Comment）
proto/      # kvs.proto / raft.proto と生成コード
web/        # フロントエンド SPA（Vite + React + TypeScript）
```

- 書き込み（Set / Delete / Incr）はリーダーで Raft 合意を取ってから適用
- フォロワーに書き込むとリーダーのアドレスヒントが返り、API 側クライアントが自動で追従
- 各ノードは WAL と Raft ログをディスクに永続化し、再起動時に復元・キャッチアップする
- 一定数のエントリ適用ごとにスナップショットを取り、Raft ログと WAL を切り詰める
  （`-snapshot-threshold`、デフォルト 1000）。大きく遅れたノードには
  InstallSnapshot RPC でスナップショットごと転送して追いつかせる

## 起動方法（ローカル）

ターミナルを 4 つ使う場合:

```sh
make web         # 最初に一度: フロントエンドを web/dist にビルド
make run-kvs-0   # ターミナル 1
make run-kvs-1   # ターミナル 2
make run-kvs-2   # ターミナル 3
make run-api     # ターミナル 4
```

http://localhost:8080 で Web UI が開ける（`web/dist` がなければ API のみ）。

または Docker Compose で一発起動（フロントエンドのビルドも含む）:

```sh
docker compose up --build
```

### フロントエンド開発

```sh
make web-dev   # Vite dev server (:5173)。/api は :8080 にプロキシされる
```

## 動作確認

```sh
# クラスタ状態
curl -s localhost:8080/api/health

# サインアップ → リンク投稿 → 投票
TOKEN=$(curl -s -X POST localhost:8080/api/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"name":"alice","email":"alice@example.com","password":"password123"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

curl -s -X POST localhost:8080/api/links \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"url":"https://raft.github.io/","title":"The Raft Consensus Algorithm","tags":["raft"]}'

curl -s -X POST localhost:8080/api/links/1/vote -H "Authorization: Bearer $TOKEN"

curl -s 'localhost:8080/api/links?sort=popular'
```

### 障害デモ

リーダーノードのプロセスを kill しても、数百 ms で新しいリーダーが選出され
読み書きが継続できる。復帰したノードは自動でログをキャッチアップする。

```sh
curl -s localhost:8080/api/health   # リーダーを確認して kill してみる
```

## 開発

```sh
make test    # go test -race ./...
make vet     # go vet
make build   # bin/api, bin/kvs をビルド
make web     # フロントエンドを web/dist にビルド
make proto   # .proto から Go コードを再生成（protoc が必要）
```

## API 一覧

| メソッド | パス | 認証 | 説明 |
| --- | --- | --- | --- |
| POST | /api/auth/signup | - | サインアップ |
| POST | /api/auth/login | - | ログイン |
| GET | /api/links | - | 一覧（?sort=recent\|popular&tag=&q=&page=&per_page=） |
| POST | /api/links | 要 | リンク投稿 |
| GET | /api/links/:id | - | リンク詳細 |
| DELETE | /api/links/:id | 要 | リンク削除（本人のみ） |
| POST | /api/links/:id/vote | 要 | 投票トグル |
| GET | /api/links/:id/comments | - | コメント一覧 |
| POST | /api/links/:id/comments | 要 | コメント投稿（parent_id で 1 階層返信） |
| DELETE | /api/comments/:id | 要 | コメント削除（本人のみ） |
| GET | /api/users/:id | - | プロフィール |
| POST | /api/ogp | - | OGP 情報取得 |
| GET | /api/health | - | クラスタ状態 |
