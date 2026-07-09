// ログイン状態（JWT + ユーザー情報）を localStorage と同期する Context。

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { useNavigate } from "react-router-dom";

import {
  clearSession,
  saveSession,
  savedUser,
  SESSION_EXPIRED_EVENT,
} from "./api";
import type { AuthResponse, PublicUser } from "./types";

interface AuthState {
  user: PublicUser | null;
  signIn: (auth: AuthResponse) => void;
  signOut: () => void;
}

const AuthContext = createContext<AuthState>({
  user: null,
  signIn: () => {},
  signOut: () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<PublicUser | null>(savedUser);
  const navigate = useNavigate();

  const signIn = useCallback((auth: AuthResponse) => {
    saveSession(auth);
    setUser(auth.user);
  }, []);

  const signOut = useCallback(() => {
    clearSession();
    setUser(null);
  }, []);

  // API 層がトークン失効を検知したらログイン画面へ誘導する（401 の一元処理）
  useEffect(() => {
    const onExpired = () => {
      setUser(null);
      navigate("/login", { state: { expired: true } });
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, [navigate]);

  return (
    <AuthContext.Provider value={{ user, signIn, signOut }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  return useContext(AuthContext);
}
