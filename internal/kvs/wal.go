package kvs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// walRecord は WAL に追記される 1 エントリ。
// Index は Raft 経由で適用された場合のログインデックス（単体利用時は 0）。
type walRecord struct {
	Index   uint64  `json:"i"`
	Command Command `json:"c"`
}

// WAL は Write-Ahead Log。ストアへの変更操作を追記専用ファイルに永続化する。
type WAL struct {
	mu   sync.Mutex
	f    *os.File
	w    *bufio.Writer
	path string
	size int64
}

// OpenWAL は WAL ファイルを開く（なければ作成する）。
func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open wal: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat wal: %w", err)
	}
	return &WAL{f: f, w: bufio.NewWriter(f), path: path, size: st.Size()}, nil
}

// Append は 1 レコードを WAL に追記し、fsync する。
func (w *WAL) Append(index uint64, cmd Command) error {
	b, err := json.Marshal(walRecord{Index: index, Command: cmd})
	if err != nil {
		return fmt.Errorf("marshal wal record: %w", err)
	}
	b = append(b, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write(b); err != nil {
		return fmt.Errorf("write wal: %w", err)
	}
	if err := w.w.Flush(); err != nil {
		return fmt.Errorf("flush wal: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("sync wal: %w", err)
	}
	w.size += int64(len(b))
	return nil
}

// Size は現在の WAL ファイルサイズ（バイト）を返す。
func (w *WAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// Close は WAL ファイルを閉じる。
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.w.Flush(); err != nil {
		return err
	}
	return w.f.Close()
}

// ReplayWAL は WAL ファイルを先頭から読み、各レコードに fn を適用する。
// ファイルが存在しない場合は何もしない。
func ReplayWAL(path string, fn func(index uint64, cmd Command) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open wal for replay: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec walRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// 末尾の書き込み途中レコードは無視して打ち切る（クラッシュ耐性）
			return nil
		}
		if err := fn(rec.Index, rec.Command); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("scan wal: %w", err)
	}
	return nil
}
