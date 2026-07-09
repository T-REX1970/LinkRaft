package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Snapshot はステートマシンのスナップショット。
// Data の中身はステートマシン（Snapshotter）が決める不透明なバイト列。
type Snapshot struct {
	Index uint64 `json:"index"` // このスナップショットに含まれる最後のログインデックス
	Term  uint64 `json:"term"`  // Index のエントリの term
	Data  []byte `json:"data"`
}

// Snapshotter はスナップショット対応のステートマシンが実装する。
type Snapshotter interface {
	// Snapshot は現在の状態をシリアライズして返す。
	Snapshot() ([]byte, error)
	// Restore はスナップショットから状態を復元する（既存の状態は破棄）。
	Restore(index uint64, data []byte) error
	// Compacted はスナップショットが index まで永続化されたことを通知する。
	// ステートマシン側の WAL 切り詰めなどに使う。
	Compacted(index uint64) error
}

func snapshotPath(dataDir string) string {
	return filepath.Join(dataDir, "raft-snapshot.json")
}

// LoadSnapshot は DataDir からスナップショットを読む。存在しなければ (nil, nil)。
func LoadSnapshot(dataDir string) (*Snapshot, error) {
	b, err := os.ReadFile(snapshotPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return &s, nil
}

// SaveSnapshot はスナップショットを一時ファイル + rename でアトミックに書き込む。
func SaveSnapshot(dataDir string, s *Snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	path := snapshotPath(dataDir)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create snapshot tmp: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("write snapshot tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync snapshot tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close snapshot tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename snapshot: %w", err)
	}
	return nil
}
