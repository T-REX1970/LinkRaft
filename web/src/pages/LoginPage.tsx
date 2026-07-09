import { useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";

import { api } from "../api";
import { useAuth } from "../auth";
import { usePageTitle } from "../usePageTitle";

export default function LoginPage() {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  usePageTitle("ログイン");
  // 401 一元処理（トークン失効）から誘導された場合の通知
  const expired = (location.state as { expired?: boolean } | null)?.expired;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      signIn(await api.login(email, password));
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <div className="form-page narrow">
      <h1>ログイン</h1>
      {expired && (
        <p className="notice">
          セッションの有効期限が切れました。再度ログインしてください。
        </p>
      )}
      <form onSubmit={submit}>
        <label>
          メールアドレス
          <input
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label>
          パスワード
          <input
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="button primary" disabled={busy}>
          ログイン
        </button>
      </form>
      <p>
        アカウントがない場合は <Link to="/signup">サインアップ</Link>
      </p>
    </div>
  );
}
