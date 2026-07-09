import { Link as RouterLink, useNavigate } from "react-router-dom";

import { api, ApiError } from "../api";
import { useAuth } from "../auth";
import { hostOf, timeAgo } from "../format";
import type { Link } from "../types";

interface Props {
  link: Link;
  // 投票後に vote_count / voted を反映するためのコールバック
  onVoted: (linkID: number, voteCount: number) => void;
}

export default function LinkCard({ link, onVoted }: Props) {
  const { user } = useAuth();
  const navigate = useNavigate();

  const vote = async () => {
    if (!user) {
      navigate("/login");
      return;
    }
    try {
      const res = await api.toggleVote(link.id);
      onVoted(link.id, res.vote_count);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) navigate("/login");
    }
  };

  return (
    <article className="link-card">
      <button className="vote-button" onClick={vote} title="投票する">
        ▲<span className="vote-count">{link.vote_count}</span>
      </button>
      <div className="link-body">
        <div className="link-title-row">
          <a href={link.url} target="_blank" rel="noreferrer" className="link-title">
            {link.title}
          </a>
          <span className="link-host">({hostOf(link.url)})</span>
        </div>
        {link.description && <p className="link-desc">{link.description}</p>}
        <div className="link-meta">
          {link.tags.map((t) => (
            <RouterLink key={t} to={`/?tag=${encodeURIComponent(t)}`} className="tag">
              {t}
            </RouterLink>
          ))}
          <span>
            by{" "}
            <RouterLink to={`/users/${link.user_id}`}>{link.user_name}</RouterLink>
          </span>
          <span>{timeAgo(link.created_at)}</span>
          <RouterLink to={`/links/${link.id}`}>
            コメント {link.comment_count}
          </RouterLink>
        </div>
      </div>
    </article>
  );
}
