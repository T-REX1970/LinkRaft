package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State は Raft ノードの状態。
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "unknown"
	}
}

// meta は永続化が必須の Raft 状態（currentTerm と votedFor）。
type meta struct {
	Term     uint64 `json:"term"`
	VotedFor string `json:"voted_for"`
}

// loadMeta は meta ファイルを読む。存在しない場合はゼロ値を返す。
func loadMeta(path string) (meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return meta{}, nil
		}
		return meta{}, fmt.Errorf("read meta: %w", err)
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return meta{}, fmt.Errorf("unmarshal meta: %w", err)
	}
	return m, nil
}

// saveMeta は meta を一時ファイル + rename でアトミックに書き込む。
func saveMeta(path string, m meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write meta tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename meta: %w", err)
	}
	// ディレクトリの fsync までは行わない（学習用途の簡略化）
	_ = filepath.Dir(path)
	return nil
}
