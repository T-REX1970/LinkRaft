package model

import "time"

// User は KVS の user:{user_id} に保存されるユーザー。
type User struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

// PublicUser は API レスポンス用のユーザー表現（メールアドレスとハッシュを含まない）。
type PublicUser struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Public はレスポンスとして返してよい表現に変換する。
func (u User) Public() PublicUser {
	return PublicUser{ID: u.ID, Name: u.Name, CreatedAt: u.CreatedAt}
}
