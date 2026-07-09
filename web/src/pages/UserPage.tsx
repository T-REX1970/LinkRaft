import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";

import { api } from "../api";
import LinkCard from "../components/LinkCard";
import type { UserProfileResponse } from "../types";

export default function UserPage() {
  const { id } = useParams();
  const userID = Number(id);
  const [data, setData] = useState<UserProfileResponse | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .getUser(userID)
      .then((res) => {
        if (!cancelled) setData(res);
      })
      .catch((e) => {
        if (!cancelled) setError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [userID]);

  if (error) return <p className="error">{error}</p>;
  if (!data) return <p>読み込み中…</p>;

  return (
    <div>
      <h1>{data.user.name}</h1>
      <p className="profile-stats">
        投稿 {data.links.length} 件 ・ 獲得投票 {data.total_votes} ・ 登録{" "}
        {new Date(data.user.created_at).toLocaleDateString("ja-JP")}
      </p>
      {data.links.length === 0 && <p>まだ投稿がありません。</p>}
      {data.links.map((l) => (
        <LinkCard
          key={l.id}
          link={l}
          onVoted={(linkID, count) =>
            setData((prev) =>
              prev
                ? {
                    ...prev,
                    links: prev.links.map((x) =>
                      x.id === linkID ? { ...x, vote_count: count } : x,
                    ),
                  }
                : prev,
            )
          }
        />
      ))}
    </div>
  );
}
