package model

import "time"

// Comment は KVS の comment:{comment_id} に保存されるコメント。
// ParentID が nil ならトップレベル、そうでなければ 1 階層の返信。
// UserName は表示用の非正規化フィールド。
type Comment struct {
	ID        int64     `json:"id"`
	LinkID    int64     `json:"link_id"`
	UserID    int64     `json:"user_id"`
	UserName  string    `json:"user_name"`
	Body      string    `json:"body"`
	ParentID  *int64    `json:"parent_id"`
	VoteCount int       `json:"vote_count"`
	CreatedAt time.Time `json:"created_at"`
}
