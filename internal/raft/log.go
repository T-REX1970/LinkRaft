package raft

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// エントリの種別。
const (
	EntryNormal uint32 = 0 // 通常のコマンド / no-op
	EntryConfig uint32 = 1 // メンバーシップ変更（Command は全メンバーの id -> addr の JSON）
)

// Entry は Raft ログの 1 エントリ。Index は 1 始まり。
type Entry struct {
	Term    uint64 `json:"t"`
	Index   uint64 `json:"i"`
	Command []byte `json:"c,omitempty"`
	Type    uint32 `json:"y,omitempty"`
}

// Log は Raft ログ。メモリ上に全エントリを保持し、追記専用ファイルに永続化する。
// スナップショットで snapIndex 以前のエントリは破棄済み（ログコンパクション）。
// ゴルーチン安全ではないので呼び出し側（Node）のロックで保護すること。
type Log struct {
	path      string
	f         *os.File
	snapIndex uint64  // スナップショットに含まれる最後のインデックス（なければ 0）
	snapTerm  uint64  // snapIndex のエントリの term
	entries   []Entry // entries[0].Index == snapIndex+1
}

// OpenLog はログファイルを読み込んで Log を作る。
// snapIndex / snapTerm はスナップショットの境界（なければ 0, 0）。
// スナップショット済みの古いエントリがファイルに残っていた場合は読み飛ばす
// （スナップショット保存とログ切り詰めの間でクラッシュした場合に起きる）。
func OpenLog(path string, snapIndex, snapTerm uint64) (*Log, error) {
	l := &Log{path: path, snapIndex: snapIndex, snapTerm: snapTerm}
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
			if e.Index <= snapIndex {
				continue // スナップショットで代替済み
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

// SnapIndex はスナップショットに含まれる最後のインデックスを返す。
func (l *Log) SnapIndex() uint64 { return l.snapIndex }

// SnapTerm は SnapIndex のエントリの term を返す。
func (l *Log) SnapTerm() uint64 { return l.snapTerm }

// LastIndex は最後のエントリのインデックスを返す（ログが空ならスナップショット境界）。
func (l *Log) LastIndex() uint64 {
	if len(l.entries) == 0 {
		return l.snapIndex
	}
	return l.entries[len(l.entries)-1].Index
}

// LastTerm は最後のエントリの term を返す（ログが空ならスナップショットの term）。
func (l *Log) LastTerm() uint64 {
	if len(l.entries) == 0 {
		return l.snapTerm
	}
	return l.entries[len(l.entries)-1].Term
}

// TermAt は index のエントリの term を返す。
// index がスナップショット境界ならスナップショットの term、範囲外なら 0。
func (l *Log) TermAt(index uint64) uint64 {
	if index == l.snapIndex {
		return l.snapTerm
	}
	e, ok := l.At(index)
	if !ok {
		return 0
	}
	return e.Term
}

// At は index のエントリを返す。スナップショット済みの範囲は取得できない。
func (l *Log) At(index uint64) (Entry, bool) {
	if index <= l.snapIndex || index > l.LastIndex() {
		return Entry{}, false
	}
	return l.entries[index-l.snapIndex-1], true
}

// From は index 以降のエントリのコピーを返す。
// index がスナップショット済みの範囲を指す場合は保持している先頭から返すので、
// 呼び出し側は SnapIndex を見て InstallSnapshot が必要か判断すること。
func (l *Log) From(index uint64) []Entry {
	if index <= l.snapIndex {
		index = l.snapIndex + 1
	}
	if index > l.LastIndex() {
		return nil
	}
	src := l.entries[index-l.snapIndex-1:]
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// LatestConfig は保持しているエントリのうち最後の設定変更エントリを返す。
func (l *Log) LatestConfig() (Entry, bool) {
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Type == EntryConfig {
			return l.entries[i], true
		}
	}
	return Entry{}, false
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
	if index <= l.snapIndex {
		index = l.snapIndex + 1 // スナップショット済みの範囲は削除できない
	}
	if index > l.LastIndex() {
		return nil
	}
	l.entries = l.entries[:index-l.snapIndex-1]
	return l.rewrite()
}

// CompactTo は index 以前のエントリを破棄してスナップショット境界を進める
// （ログコンパクション）。term は index のエントリの term。
func (l *Log) CompactTo(index, term uint64) error {
	if index <= l.snapIndex {
		return nil
	}
	if index >= l.LastIndex() {
		l.entries = nil
	} else {
		// 前方を削るのでスライスを作り直して古い配列への参照を切る
		rest := l.entries[index-l.snapIndex:]
		l.entries = append([]Entry(nil), rest...)
	}
	l.snapIndex = index
	l.snapTerm = term
	return l.rewrite()
}

// rewrite は保持中のエントリでログファイルを書き直す。
func (l *Log) rewrite() error {
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
