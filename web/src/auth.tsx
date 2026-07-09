// ログイン状態（JWT + ユーザー情報）を localStorage と同期する Context。

import {
  createContext,
  useCallback,
  useContext,
  useState,
  type ReactNode,
} from "react";

import { clearSession, saveSession, savedUser } from "./api";
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

  const signIn = useCallback((auth: AuthResponse) => {
    saveSession(auth);
    setUser(auth.user);
  }, []);

  const signOut = useCallback(() => {
    clearSession();
    setUser(null);
  }, []);

  return (
    <AuthContext.Provider value={{ user, signIn, signOut }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  return useContext(AuthContext);
}
