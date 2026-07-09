package raft

import (
	"fmt"
	"path/filepath"
	"testing"
)

func newTestLog(t *testing.T, n int) (*Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raft-log.jsonl")
	l, err := OpenLog(path, 0, 0)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	for i := 1; i <= n; i++ {
		e := Entry{Term: 1, Index: uint64(i), Command: []byte(fmt.Sprintf("cmd-%d", i))}
		if err := l.Append(e); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}
	return l, path
}

func TestLogCompactTo(t *testing.T) {
	l, path := newTestLog(t, 10)
	defer l.Close()

	if err := l.CompactTo(5, 1); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}

	if got := l.SnapIndex(); got != 5 {
		t.Fatalf("SnapIndex = %d, want 5", got)
	}
	if got := l.LastIndex(); got != 10 {
		t.Fatalf("LastIndex = %d, want 10", got)
	}
	if _, ok := l.At(5); ok {
		t.Fatal("At(5) should be gone after compaction")
	}
	if e, ok := l.At(6); !ok || string(e.Command) != "cmd-6" {
		t.Fatalf("At(6) = %v, %v", e, ok)
	}
	if got := l.TermAt(5); got != 1 {
		t.Fatalf("TermAt(snapIndex) = %d, want 1", got)
	}
	// From はコンパクション済みの範囲を要求されたら保持分の先頭から返す
	if got := l.From(3); len(got) != 5 || got[0].Index != 6 {
		t.Fatalf("From(3) = %d entries starting %d, want 5 starting 6", len(got), got[0].Index)
	}

	// ファイルにも反映されている（再起動シミュレート）
	l2, err := OpenLog(path, 5, 1)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()
	if l2.LastIndex() != 10 || l2.SnapIndex() != 5 {
		t.Fatalf("reopened LastIndex=%d SnapIndex=%d, want 10, 5", l2.LastIndex(), l2.SnapIndex())
	}
}

func TestLogCompactToAll(t *testing.T) {
	l, _ := newTestLog(t, 10)
	defer l.Close()

	if err := l.CompactTo(10, 1); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if got := l.LastIndex(); got != 10 {
		t.Fatalf("LastIndex = %d, want 10 (snapshot boundary)", got)
	}
	if got := l.LastTerm(); got != 1 {
		t.Fatalf("LastTerm = %d, want 1", got)
	}
	// コンパクション後も追記できる
	if err := l.Append(Entry{Term: 2, Index: 11, Command: []byte("x")}); err != nil {
		t.Fatalf("Append after compaction: %v", err)
	}
	if e, ok := l.At(11); !ok || e.Term != 2 {
		t.Fatalf("At(11) = %v, %v", e, ok)
	}
}

func TestOpenLogSkipsCompactedEntries(t *testing.T) {
	// スナップショット保存とログ切り詰めの間でクラッシュした状況:
	// ファイルには古いエントリが残っているが、境界以下は読み飛ばされる
	l, path := newTestLog(t, 10)
	l.Close()

	l2, err := OpenLog(path, 7, 1)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	defer l2.Close()
	if _, ok := l2.At(7); ok {
		t.Fatal("At(7) should be skipped")
	}
	if e, ok := l2.At(8); !ok || string(e.Command) != "cmd-8" {
		t.Fatalf("At(8) = %v, %v", e, ok)
	}
	if l2.LastIndex() != 10 {
		t.Fatalf("LastIndex = %d, want 10", l2.LastIndex())
	}
}

func TestLogTruncateFromAfterCompaction(t *testing.T) {
	l, _ := newTestLog(t, 10)
	defer l.Close()
	if err := l.CompactTo(5, 1); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if err := l.TruncateFrom(8); err != nil {
		t.Fatalf("TruncateFrom: %v", err)
	}
	if got := l.LastIndex(); got != 7 {
		t.Fatalf("LastIndex = %d, want 7", got)
	}
}
