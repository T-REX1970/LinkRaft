import { useNavigate } from "react-router-dom";

import { api, ApiError } from "../api";
import { useAuth } from "../auth";
import type { Link } from "../types";

interface Props {
  link: Link;
  // 投票トグル後の反映用（vote_count と voted の両方を渡す）
  onVoted: (linkID: number, voteCount: number, voted: boolean) => void;
}

// 投票トグルボタン。投票済みなら塗りつぶし表示になる。
export default function VoteButton({ link, onVoted }: Props) {
  const { user } = useAuth();
  const navigate = useNavigate();

  const vote = async () => {
    if (!user) {
      navigate("/login");
      return;
    }
    try {
      const res = await api.toggleVote(link.id);
      onVoted(link.id, res.vote_count, res.voted);
    } catch (e) {
      // 401 は api.ts が一元処理してログインへ誘導する
      if (!(e instanceof ApiError && e.status === 401)) throw e;
    }
  };

  return (
    <button
      className={link.voted ? "vote-button voted" : "vote-button"}
      onClick={vote}
      aria-pressed={link.voted}
      aria-label={link.voted ? "投票を取り消す" : "投票する"}
      title={link.voted ? "投票を取り消す" : "投票する"}
    >
      ▲<span className="vote-count">{link.vote_count}</span>
    </button>
  );
}
