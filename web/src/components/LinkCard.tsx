import { Link as RouterLink } from "react-router-dom";

import { hostOf, timeAgo } from "../format";
import type { Link } from "../types";
import VoteButton from "./VoteButton";

interface Props {
  link: Link;
  onVoted: (linkID: number, voteCount: number, voted: boolean) => void;
}

export default function LinkCard({ link, onVoted }: Props) {
  return (
    <article className="link-card">
      <VoteButton link={link} onVoted={onVoted} />
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
      {link.image_url && (
        <img
          className="link-thumb"
          src={link.image_url}
          alt=""
          loading="lazy"
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
      )}
    </article>
  );
}
