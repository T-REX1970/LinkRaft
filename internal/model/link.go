package model

import "time"

// Link は KVS の link:{link_id} に保存されるリンク。
// UserName は表示用の非正規化フィールド（KVS には JOIN がないため投稿時に埋める）。
type Link struct {
	ID           int64     `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	UserID       int64     `json:"user_id"`
	UserName     string    `json:"user_name"`
	Tags         []string  `json:"tags"`
	VoteCount    int       `json:"vote_count"`
	CommentCount int       `json:"comment_count"`
	CreatedAt    time.Time `json:"created_at"`
}
