package raft

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Entry は Raft ログの 1 エントリ。Index は 1 始まり。
type Entry struct {
	Term    uint64 `json:"t"`
	Index   uint64 `json:"i"`
	Command []byte `json:"c,omitempty"`
}

// Log は Raft ログ。メモリ上に全エントリを保持し、追記専用ファイルに永続化する。
// スナップショットは未実装（学習用途）。ゴルーチン安全ではないので
// 呼び出し側（Node）のロックで保護すること。
type Log struct {
	path    string
	f       *os.File
	entries []Entry // entries[0].Index == 1
}

// OpenLog はログファイルを読み込んで Log を作る。
func OpenLog(path string) (*Log, error) {
	l := &Log{path: path}
	f, err := os.Open(path)
	if err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil {
				break // 書き込み途中の末尾レコードは捨てる
			}
			l.entries = append(l.entries, e)
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("scan raft log: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open raft log: %w", err)
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open raft log for append: %w", err)
	}
	l.f = w
	return l, nil
}

// LastIndex は最後のエントリのインデックスを返す（空なら 0）。
func (l *Log) LastIndex() uint64 {
	if len(l.entries) == 0 {
		return 0
	}
	return l.entries[len(l.entries)-1].Index
}

// LastTerm は最後のエントリの term を返す（空なら 0）。
func (l *Log) LastTerm() uint64 {
	if len(l.entries) == 0 {
		return 0
	}
	return l.entries[len(l.entries)-1].Term
}

// TermAt は index のエントリの term を返す。index==0 または範囲外なら 0。
func (l *Log) TermAt(index uint64) uint64 {
	e, ok := l.At(index)
	if !ok {
		return 0
	}
	return e.Term
}

// At は index のエントリを返す。
func (l *Log) At(index uint64) (Entry, bool) {
	if index == 0 || index > l.LastIndex() {
		return Entry{}, false
	}
	return l.entries[index-1], true
}

// From は index 以降のエントリのコピーを返す。
func (l *Log) From(index uint64) []Entry {
	if index == 0 {
		index = 1
	}
	if index > l.LastIndex() {
		return nil
	}
	src := l.entries[index-1:]
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// Append はエントリを追記して永続化する。
func (l *Log) Append(entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	var buf []byte
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal raft entry: %w", err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if _, err := l.f.Write(buf); err != nil {
		return fmt.Errorf("write raft log: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("sync raft log: %w", err)
	}
	l.entries = append(l.entries, entries...)
	return nil
}

// TruncateFrom は index 以降のエントリを削除する（リーダーとの競合解消用）。
// ファイルは全体を書き直す。競合は稀なので許容する。
func (l *Log) TruncateFrom(index uint64) error {
	if index == 0 || index > l.LastIndex() {
		return nil
	}
	l.entries = l.entries[:index-1]

	if err := l.f.Close(); err != nil {
		return fmt.Errorf("close raft log: %w", err)
	}
	tmp := l.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create raft log tmp: %w", err)
	}
	w := bufio.NewWriter(f)
	for _, e := range l.entries {
		b, err := json.Marshal(e)
		if err != nil {
			f.Close()
			return fmt.Errorf("marshal raft entry: %w", err)
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("flush raft log tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync raft log tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close raft log tmp: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		return fmt.Errorf("rename raft log: %w", err)
	}
	nf, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("reopen raft log: %w", err)
	}
	l.f = nf
	return nil
}

// Close はログファイルを閉じる。
func (l *Log) Close() error {
	return l.f.Close()
}
