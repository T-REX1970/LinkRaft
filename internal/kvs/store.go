package kvs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// 操作種別。Raft ログのコマンドとしてもシリアライズされる。
const (
	OpSet    = "set"
	OpDelete = "delete"
	OpIncr   = "incr"
)

// Command はストアへの変更操作。Raft のログエントリとして複製される。
type Command struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value []byte `json:"value,omitempty"`
}

// EncodeCommand は Command を Raft ログ用にシリアライズする。
func EncodeCommand(c Command) ([]byte, error) {
	return json.Marshal(c)
}

// DecodeCommand は Raft ログエントリから Command を復元する。
func DecodeCommand(b []byte) (Command, error) {
	var c Command
	if err := json.Unmarshal(b, &c); err != nil {
		return Command{}, fmt.Errorf("decode command: %w", err)
	}
	return c, nil
}

// Store はインメモリのキーバリューストア。
// WAL を与えると変更操作を永続化し、再起動時に復元できる。
// Raft のステートマシンとしても機能する（Apply を参照）。
type Store struct {
	mu           sync.RWMutex
	data         map[string][]byte
	wal          *WAL
	appliedIndex uint64
}

// NewStore は空のストアを作る。wal は nil でもよい（永続化なし）。
func NewStore(wal *WAL) *Store {
	return &Store{data: make(map[string][]byte), wal: wal}
}

// OpenStore は WAL を開いて内容を再生し、永続化付きストアを返す。
func OpenStore(walPath string) (*Store, error) {
	s := &Store{data: make(map[string][]byte)}
	err := ReplayWAL(walPath, func(index uint64, cmd Command) error {
		s.applyLocked(cmd)
		if index > s.appliedIndex {
			s.appliedIndex = index
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("replay wal: %w", err)
	}
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, err
	}
	s.wal = wal
	return s, nil
}

// Get はキーの値を返す。
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set はキーに値を保存する。
func (s *Store) Set(key string, value []byte) error {
	_, err := s.Apply(0, Command{Op: OpSet, Key: key, Value: value})
	return err
}

// Delete はキーを削除する。
func (s *Store) Delete(key string) error {
	_, err := s.Apply(0, Command{Op: OpDelete, Key: key})
	return err
}

// Incr はキーの整数値を 1 増やし、増加後の値を返す。存在しない場合は 1 になる。
func (s *Store) Incr(key string) (int64, error) {
	res, err := s.Apply(0, Command{Op: OpIncr, Key: key})
	if err != nil {
		return 0, err
	}
	return res.(int64), nil
}

// Keys は prefix で始まるキーの一覧をソート済みで返す。
func (s *Store) Keys(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, 16)
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Len は保持しているキー数を返す。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// AppliedIndex は適用済みの最大 Raft ログインデックスを返す。
func (s *Store) AppliedIndex() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appliedIndex
}

// Apply はコマンドをストアに適用する。Raft から呼ばれる場合は index に
// ログインデックスが入る（単体利用時は 0）。適用結果を返す（Incr は増加後の値）。
// index が適用済み以下の場合は WAL 再生との重複適用を避けるためスキップする。
func (s *Store) Apply(index uint64, cmd Command) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index != 0 && index <= s.appliedIndex {
		// WAL から復元済みのエントリ。現在値を返す。
		if cmd.Op == OpIncr {
			return s.currentInt(cmd.Key), nil
		}
		return nil, nil
	}
	res := s.applyLocked(cmd)
	if s.wal != nil {
		if err := s.wal.Append(index, cmd); err != nil {
			return nil, fmt.Errorf("wal append: %w", err)
		}
	}
	if index > s.appliedIndex {
		s.appliedIndex = index
	}
	return res, nil
}

func (s *Store) applyLocked(cmd Command) any {
	switch cmd.Op {
	case OpSet:
		s.data[cmd.Key] = cmd.Value
		return nil
	case OpDelete:
		delete(s.data, cmd.Key)
		return nil
	case OpIncr:
		n := s.currentInt(cmd.Key) + 1
		s.data[cmd.Key] = []byte(strconv.FormatInt(n, 10))
		return n
	default:
		return nil
	}
}

// currentInt は key の現在の整数値を返す。s.mu を保持した状態で呼ぶこと。
func (s *Store) currentInt(key string) int64 {
	v, ok := s.data[key]
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// WALSize は WAL のサイズ（バイト）を返す。WAL なしの場合は 0。
func (s *Store) WALSize() int64 {
	if s.wal == nil {
		return 0
	}
	return s.wal.Size()
}

// Close はストアを閉じる。
func (s *Store) Close() error {
	if s.wal != nil {
		return s.wal.Close()
	}
	return nil
}
