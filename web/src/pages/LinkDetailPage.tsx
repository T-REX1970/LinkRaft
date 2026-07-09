import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link as RouterLink, useNavigate, useParams } from "react-router-dom";

import { api } from "../api";
import { useAuth } from "../auth";
import ConfirmDialog from "../components/ConfirmDialog";
import VoteButton from "../components/VoteButton";
import { hostOf, timeAgo } from "../format";
import type { Comment, Link } from "../types";
import { usePageTitle } from "../usePageTitle";

// 確認ダイアログで実行する削除操作
interface PendingDelete {
  message: string;
  run: () => Promise<void>;
}

export default function LinkDetailPage() {
  const { id } = useParams();
  const linkID = Number(id);
  const navigate = useNavigate();
  const { user } = useAuth();

  const [link, setLink] = useState<Link | null>(null);
  const [comments, setComments] = useState<Comment[]>([]);
  const [error, setError] = useState("");
  const [pending, setPending] = useState<PendingDelete | null>(null);

  usePageTitle(link?.title ?? "");

  const reload = useCallback(async () => {
    try {
      const [l, c] = await Promise.all([
        api.getLink(linkID),
        api.listComments(linkID),
      ]);
      setLink(l.link);
      setComments(c.comments);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [linkID]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const confirmDeleteLink = () =>
    setPending({
      message: "このリンクを削除しますか？コメントもすべて削除されます。",
      run: async () => {
        await api.deleteLink(linkID);
        navigate("/");
      },
    });

  const confirmDeleteComment = (commentID: number) =>
    setPending({
      message: "このコメントを削除しますか？",
      run: async () => {
        await api.deleteComment(commentID);
        await reload();
      },
    });

  if (error) return <p className="error">{error}</p>;
  if (!link) return <p>読み込み中…</p>;

  const topLevel = comments.filter((c) => c.parent_id === null);
  const repliesOf = (pid: number) => comments.filter((c) => c.parent_id === pid);

  return (
    <div>
      <article className="link-card detail">
        <VoteButton
          link={link}
          onVoted={(_, count, voted) =>
            setLink((prev) =>
              prev ? { ...prev, vote_count: count, voted } : prev,
            )
          }
        />
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
            {user?.id === link.user_id && (
              <button className="button danger small" onClick={confirmDeleteLink}>
                削除
              </button>
            )}
          </div>
        </div>
        {link.image_url && (
          <img
            className="link-thumb"
            src={link.image_url}
            alt=""
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
        )}
      </article>

      <section className="comments">
        <h2>コメント ({comments.length})</h2>
        {user ? (
          <CommentForm linkID={linkID} parentID={null} onPosted={reload} />
        ) : (
          <p>
            コメントするには <RouterLink to="/login">ログイン</RouterLink> してください。
          </p>
        )}
        {topLevel.map((c) => (
          <CommentItem
            key={c.id}
            comment={c}
            replies={repliesOf(c.id)}
            linkID={linkID}
            onDelete={confirmDeleteComment}
            onChanged={reload}
          />
        ))}
      </section>

      <ConfirmDialog
        open={pending !== null}
        message={pending?.message ?? ""}
        onCancel={() => setPending(null)}
        onConfirm={async () => {
          const p = pending;
          setPending(null);
          if (!p) return;
          try {
            await p.run();
          } catch (e) {
            setError(e instanceof Error ? e.message : String(e));
          }
        }}
      />
    </div>
  );
}

function CommentItem({
  comment,
  replies,
  linkID,
  onDelete,
  onChanged,
}: {
  comment: Comment;
  replies: Comment[];
  linkID: number;
  onDelete: (commentID: number) => void;
  onChanged: () => Promise<void>;
}) {
  const { user } = useAuth();
  const [replying, setReplying] = useState(false);

  return (
    <div className="comment">
      <div className="comment-head">
        <RouterLink to={`/users/${comment.user_id}`}>{comment.user_name}</RouterLink>
        <span className="comment-time">{timeAgo(comment.created_at)}</span>
        {user?.id === comment.user_id && (
          <button className="button ghost small" onClick={() => onDelete(comment.id)}>
            削除
          </button>
        )}
      </div>
      <p className="comment-body">{comment.body}</p>
      {user && !replying && (
        <button className="button ghost small" onClick={() => setReplying(true)}>
          返信
        </button>
      )}
      {replying && (
        <CommentForm
          linkID={linkID}
          parentID={comment.id}
          onPosted={async () => {
            setReplying(false);
            await onChanged();
          }}
        />
      )}
      {replies.length > 0 && (
        <div className="replies">
          {replies.map((r) => (
            <div key={r.id} className="comment reply">
              <div className="comment-head">
                <RouterLink to={`/users/${r.user_id}`}>{r.user_name}</RouterLink>
                <span className="comment-time">{timeAgo(r.created_at)}</span>
                {user?.id === r.user_id && (
                  <button className="button ghost small" onClick={() => onDelete(r.id)}>
                    削除
                  </button>
                )}
              </div>
              <p className="comment-body">{r.body}</p>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function CommentForm({
  linkID,
  parentID,
  onPosted,
}: {
  linkID: number;
  parentID: number | null;
  onPosted: () => Promise<void>;
}) {
  const [body, setBody] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!body.trim()) return;
    setBusy(true);
    try {
      await api.createComment(linkID, body.trim(), parentID);
      setBody("");
      setError("");
      await onPosted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="comment-form">
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder={parentID ? "返信を書く…" : "コメントを書く…"}
        rows={3}
        maxLength={2000}
      />
      {error && <p className="error">{error}</p>}
      <button className="button primary small" disabled={busy || !body.trim()}>
        投稿
      </button>
    </form>
  );
}
