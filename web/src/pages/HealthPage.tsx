import { useEffect, useState } from "react";

import { api } from "../api";
import type { HealthResponse } from "../types";

// KVS クラスタの状態を 2 秒間隔でポーリング表示する。
// リーダーを kill すると再選出される様子がここで観察できる。
export default function HealthPage() {
  const [data, setData] = useState<HealthResponse | null>(null);
  const [error, setError] = useState("");

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

  return (
    <div>
      <h1>KVS クラスタ状態</h1>
      <p>
        Raft 3 ノード構成。リーダーのプロセスを kill しても数百 ms
        で新リーダーが選出されます（2 秒ごとに自動更新）。
      </p>
      {error && <p className="error">{error}</p>}
      {data && (
        <table className="health-table">
          <thead>
            <tr>
              <th>ノード</th>
              <th>アドレス</th>
              <th>状態</th>
            </tr>
          </thead>
          <tbody>
            {data.nodes.map((n) => (
              <tr key={n.id}>
                <td>
                  {n.id}
                  {n.is_leader && " 👑"}
                </td>
                <td>{n.address}</td>
                <td>
                  <span className={`state state-${n.state}`}>{n.state}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
