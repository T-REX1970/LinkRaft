// Go API (/api/*) への薄い fetch ラッパー。
// 認証切れ（401）はここで一元処理し、セッションを破棄して
// "linkraft:session-expired" イベントを発火する（AuthProvider が購読）。

import { translateError } from "./errors";
import type {
  AuthResponse,
  Comment,
  HealthResponse,
  Link,
  LinkListResponse,
  OGPResponse,
  Tag,
  UserProfileResponse,
} from "./types";

const TOKEN_KEY = "linkraft.token";
const USER_KEY = "linkraft.user";

export function savedToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function saveSession(auth: AuthResponse): void {
  localStorage.setItem(TOKEN_KEY, auth.token);
  localStorage.setItem(USER_KEY, JSON.stringify(auth.user));
}

export function savedUser(): AuthResponse["user"] | null {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

export function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export const SESSION_EXPIRED_EVENT = "linkraft:session-expired";

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {};
  const token = savedToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 204) return undefined as T;

  let data: unknown;
  try {
    data = await res.json();
  } catch {
    data = {};
  }
  if (!res.ok) {
    // トークンが無効になった（期限切れなど）: セッションを破棄してログインへ誘導。
    // ログイン・サインアップ自体の 401 は通常のエラーとして呼び出し元に返す。
    if (res.status === 401 && token && !path.startsWith("/api/auth/")) {
      clearSession();
      window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
    }
    const msg =
      typeof data === "object" && data !== null && "message" in data
        ? String((data as { message: unknown }).message)
        : res.statusText;
    throw new ApiError(res.status, translateError(msg, res.status));
  }
  return data as T;
}

export const api = {
  signup: (name: string, email: string, password: string) =>
    request<AuthResponse>("POST", "/api/auth/signup", { name, email, password }),
  login: (email: string, password: string) =>
    request<AuthResponse>("POST", "/api/auth/login", { email, password }),

  listLinks: (params: {
    sort?: "recent" | "popular";
    tag?: string;
    q?: string;
    page?: number;
  }) => {
    const qs = new URLSearchParams();
    if (params.sort) qs.set("sort", params.sort);
    if (params.tag) qs.set("tag", params.tag);
    if (params.q) qs.set("q", params.q);
    if (params.page && params.page > 1) qs.set("page", String(params.page));
    const suffix = qs.toString() ? `?${qs}` : "";
    return request<LinkListResponse>("GET", `/api/links${suffix}`);
  },
  getLink: (id: number) =>
    request<{ link: Link }>("GET", `/api/links/${id}`),
  createLink: (input: {
    url: string;
    title: string;
    description: string;
    image_url: string;
    tags: string[];
  }) => request<{ link: Link }>("POST", "/api/links", input),
  deleteLink: (id: number) => request<void>("DELETE", `/api/links/${id}`),
  toggleVote: (id: number) =>
    request<{ voted: boolean; vote_count: number }>(
      "POST",
      `/api/links/${id}/vote`,
    ),

  listComments: (linkID: number) =>
    request<{ comments: Comment[] }>("GET", `/api/links/${linkID}/comments`),
  createComment: (linkID: number, body: string, parentID: number | null) =>
    request<{ comment: Comment }>("POST", `/api/links/${linkID}/comments`, {
      body,
      parent_id: parentID,
    }),
  deleteComment: (id: number) => request<void>("DELETE", `/api/comments/${id}`),

  getUser: (id: number) =>
    request<UserProfileResponse>("GET", `/api/users/${id}`),
  fetchOGP: (url: string) => request<OGPResponse>("POST", "/api/ogp", { url }),
  health: () => request<HealthResponse>("GET", "/api/health"),
  listTags: () => request<{ tags: Tag[] | null }>("GET", "/api/tags"),

  addClusterMember: (id: string, addr: string) =>
    request<{ ok: boolean }>("POST", "/api/cluster/members", { id, addr }),
  removeClusterMember: (id: string) =>
    request<{ ok: boolean }>("DELETE", `/api/cluster/members/${id}`),
};
