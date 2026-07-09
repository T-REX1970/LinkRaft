// API レスポンスの型定義（internal/model と対応）

export interface PublicUser {
  id: number;
  name: string;
  created_at: string;
}

export interface Link {
  id: number;
  url: string;
  title: string;
  description: string;
  user_id: number;
  user_name: string;
  tags: string[];
  vote_count: number;
  comment_count: number;
  created_at: string;
}

export interface Comment {
  id: number;
  link_id: number;
  user_id: number;
  user_name: string;
  body: string;
  parent_id: number | null;
  vote_count: number;
  created_at: string;
}

export interface LinkListResponse {
  links: Link[];
  total: number;
  page: number;
  per_page: number;
}

export interface AuthResponse {
  user: PublicUser;
  token: string;
}

export interface UserProfileResponse {
  user: PublicUser;
  links: Link[];
  total_votes: number;
}

export interface OGPResponse {
  title: string;
  description: string;
  image: string;
}

export interface HealthNode {
  id: string;
  address: string;
  state: "leader" | "follower" | "candidate" | "down";
  is_leader: boolean;
}

export interface HealthResponse {
  nodes: HealthNode[];
  leader_id: string;
}
