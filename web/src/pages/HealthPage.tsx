import { useEffect, useState, type FormEvent } from "react";

import { api } from "../api";
import { useAuth } from "../auth";
import type { HealthNode, HealthResponse } from "../types";
import { usePageTitle } from "../usePageTitle";

// KVS クラスタの状態を 2 秒間隔でポーリング表示する。
// リーダー選出・ログ複製の進捗・メンバーシップ変更（ノードの追加/削除）を
// ここから観察・操作できる。

function ReplicationBar({
  node,
  leaderLast,
}: {
  node: HealthNode;
  leaderLast: number;
}) {
  if (node.state === "down" || leaderLast === 0) return null;
  const match = node.is_leader ? leaderLast : node.match_index;
  const pct = Math.max(0, Math.min(100, (match / leaderLast) * 100));
  return (
    <div className="repl">
      <div className="repl-bar">
        <div
          className={`repl-fill${pct >= 100 ? " full" : ""}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="repl-label">
        複製 {match} / {leaderLast}
      </span>
    </div>
  );
}

export default function HealthPage() {
  const { user } = useAuth();
  const [data, setData] = useState<HealthResponse | null>(null);
  const [error, setError] = useState("");

  // メンバーシップ操作フォーム
  const [newID, setNewID] = useState("");
  const [newAddr, setNewAddr] = useState("");
  const [opBusy, setOpBusy] = useState(false);
  const [opError, setOpError] = useState("");
  const [opInfo, setOpInfo] = useState("");

  usePageTitle("クラスタ状態");

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .health()
        .then((res) => {
          if (!cancelled) {
            setData(res);
            setError("");
          }
        })
        .catch((e) => {
          if (!cancelled) setError(e.message);
        });
    void load();
    const timer = setInterval(load, 2000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  const addMember = async (e: FormEvent) => {
    e.preventDefault();
    setOpBusy(true);
    setOpError("");
    setOpInfo("");
    try {
      await api.addClusterMember(newID.trim(), newAddr.trim());
      setOpInfo(`${newID.trim()} を追加しました`);
      setNewID("");
      setNewAddr("");
    } catch (err) {
      setOpError(err instanceof Error ? err.message : String(err));
    } finally {
      setOpBusy(false);
    }
  };

  const removeMember = async (id: string) => {
    if (!window.confirm(`ノード ${id} をクラスタから削除しますか？`)) return;
    setOpBusy(true);
    setOpError("");
    setOpInfo("");
    try {
      await api.removeClusterMember(id);
      setOpInfo(`${id} を削除しました`);
    } catch (err) {
      setOpError(err instanceof Error ? err.message : String(err));
    } finally {
      setOpBusy(false);
    }
  };

  const leaderLast = data?.last_log_index ?? 0;

  return (
    <div>
      <h1>KVS クラスタ状態</h1>
      <p>
        自作 Raft クラスタのライブビュー（2 秒ごとに自動更新）。リーダーを kill
        すると数百 ms で再選出され、pre-vote
        が復帰ノードによる無駄な選挙を防ぎます。読み取りは ReadIndex
        で線形化可能。ノードの追加・削除（メンバーシップ変更）もここから行えます。
      </p>
      {error && <p className="error">{error}</p>}
      {data && (
        <>
          <p className="cluster-summary">
            リーダー: <strong>{data.leader_id || "選出中…"}</strong>
            {data.term > 0 && <> ・ term {data.term}</>}
            {leaderLast > 0 && <> ・ ログ末尾 {leaderLast}</>}
          </p>
          <div className="node-grid">
            {data.nodes.map((n) => (
              <div
                key={n.id || n.address}
                className={`node-card${n.is_leader ? " leader" : ""}${
                  n.state === "down" ? " down" : ""
                }`}
              >
                <div className="node-head">
                  <span className="node-id">
                    {n.id || "?"}
                    {n.is_leader && " 👑"}
                  </span>
                  <span className={`state state-${n.state}`}>{n.state}</span>
                </div>
                <div className="node-addr">{n.address}</div>
                {!n.is_member && n.id && (
                  <div className="node-removed">構成外（削除済み）</div>
                )}
                {n.state !== "down" && (
                  <dl className="node-stats">
                    <div>
                      <dt>term</dt>
                      <dd>{n.term}</dd>
                    </div>
                    <div>
                      <dt>commit</dt>
                      <dd>{n.commit_index}</dd>
                    </div>
                    <div>
                      <dt>applied</dt>
                      <dd>{n.applied_index}</dd>
                    </div>
                    <div>
                      <dt>keys</dt>
                      <dd>{n.keys_total}</dd>
                    </div>
                    {n.snapshot_index > 0 && (
                      <div>
                        <dt>snap</dt>
                        <dd>{n.snapshot_index}</dd>
                      </div>
                    )}
                  </dl>
                )}
                {n.is_member && (
                  <ReplicationBar node={n} leaderLast={leaderLast} />
                )}
                {user && n.is_member && !n.is_leader && n.id && (
                  <button
                    type="button"
                    className="button ghost small danger-text"
                    onClick={() => void removeMember(n.id)}
                    disabled={opBusy}
                  >
                    クラスタから削除
                  </button>
                )}
              </div>
            ))}
          </div>

          {user && (
            <div className="member-form">
              <h2>ノードを追加</h2>
              <p className="hint">
                追加するノードは先に <code>-join</code>{" "}
                フラグ付きで起動しておいてください（例:{" "}
                <code>
                  kvs -id node-3 -listen :9003 -advertise localhost:9003 -join
                </code>
                ）。
              </p>
              <form onSubmit={addMember}>
                <input
                  required
                  placeholder="ノード ID（例: node-3）"
                  value={newID}
                  onChange={(e) => setNewID(e.target.value)}
                />
                <input
                  required
                  placeholder="アドレス（例: localhost:9003）"
                  value={newAddr}
                  onChange={(e) => setNewAddr(e.target.value)}
                />
                <button className="button primary" disabled={opBusy}>
                  追加
                </button>
              </form>
            </div>
          )}
          {opError && <p className="error">{opError}</p>}
          {opInfo && <p className="info">{opInfo}</p>}
        </>
      )}
    </div>
  );
}
