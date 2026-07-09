import { Link, useNavigate } from "react-router-dom";

import { useAuth } from "../auth";

export default function Header() {
  const { user, signOut } = useAuth();
  const navigate = useNavigate();

  return (
    <header className="header">
      <div className="container header-inner">
        <Link to="/" className="brand">
          ⚓ LinkRaft
        </Link>
        <nav className="nav">
          <Link to="/health" className="nav-link">
            クラスタ
          </Link>
          {user ? (
            <>
              <Link to="/submit" className="button primary small">
                ＋ 投稿
              </Link>
              <Link to={`/users/${user.id}`} className="nav-link">
                {user.name}
              </Link>
              <button
                className="button ghost small"
                onClick={() => {
                  signOut();
                  navigate("/");
                }}
              >
                ログアウト
              </button>
            </>
          ) : (
            <>
              <Link to="/login" className="nav-link">
                ログイン
              </Link>
              <Link to="/signup" className="button primary small">
                サインアップ
              </Link>
            </>
          )}
        </nav>
      </div>
    </header>
  );
}
