package model

import "time"

// Vote は KVS の vote:{link_id}:{user_id} に保存される投票（重複投票防止を兼ねる）。
type Vote struct {
	CreatedAt time.Time `json:"created_at"`
}
