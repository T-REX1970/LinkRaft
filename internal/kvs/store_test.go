package kvs

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreBasicOps(t *testing.T) {
	s := NewStore(nil)

	if err := s.Set("user:1", []byte(`{"name":"alice"}`)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := s.Get("user:1")
	if !ok || string(v) != `{"name":"alice"}` {
		t.Fatalf("Get = %q, %v", v, ok)
	}

	if err := s.Delete("user:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get("user:1"); ok {
		t.Fatal("Get after Delete should miss")
	}
}

func TestStoreIncr(t *testing.T) {
	s := NewStore(nil)
	for want := int64(1); want <= 3; want++ {
		got, err := s.Incr("seq:link")
		if err != nil {
			t.Fatalf("Incr: %v", err)
		}
		if got != want {
			t.Fatalf("Incr = %d, want %d", got, want)
		}
	}
}

func TestStoreKeys(t *testing.T) {
	s := NewStore(nil)
	for _, k := range []string{"link:2", "link:1", "user:1"} {
		if err := s.Set(k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Keys("link:")
	want := []string{"link:1", "link:2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys = %v, want %v", got, want)
	}
}

func TestStoreWALPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kvs.wal")

	s1, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := s1.Set("link:1", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Set("link:2", []byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Delete("link:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Incr("seq:link"); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// 再起動をシミュレート
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	if _, ok := s2.Get("link:1"); ok {
		t.Fatal("deleted key should stay deleted after replay")
	}
	if v, ok := s2.Get("link:2"); !ok || string(v) != "world" {
		t.Fatalf("link:2 = %q, %v", v, ok)
	}
	if n, err := s2.Incr("seq:link"); err != nil || n != 2 {
		t.Fatalf("Incr after replay = %d, %v; want 2", n, err)
	}
}

func TestStoreSnapshotRestore(t *testing.T) {
	s1 := NewStore(nil)
	if _, err := s1.Apply(1, Command{Op: OpSet, Key: "a", Value: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Apply(2, Command{Op: OpIncr, Key: "seq"}); err != nil {
		t.Fatal(err)
	}
	data, err := s1.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	s2 := NewStore(nil)
	if err := s2.Restore(2, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if v, ok := s2.Get("a"); !ok || string(v) != "1" {
		t.Fatalf("a = %q, %v", v, ok)
	}
	if n, err := s2.Incr("seq"); err != nil || n != 2 {
		t.Fatalf("Incr after restore = %d, %v; want 2", n, err)
	}
	if s2.AppliedIndex() != 2 {
		t.Fatalf("AppliedIndex = %d, want 2", s2.AppliedIndex())
	}
}

func TestOpenStoreAtSkipsCoveredWALRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kvs.wal")

	// スナップショット前の状態を WAL に書く
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Apply(1, Command{Op: OpSet, Key: "k", Value: []byte("old")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Apply(2, Command{Op: OpSet, Key: "k", Value: []byte("snap")}); err != nil {
		t.Fatal(err)
	}
	snap, err := s1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// WAL 切り詰め前にクラッシュした想定: index<=2 のレコードが残ったまま
	if _, err := s1.Apply(3, Command{Op: OpSet, Key: "k2", Value: []byte("after")}); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	// スナップショット(index=2) + WAL 再生で復元
	s2, err := OpenStoreAt(path, 2, snap)
	if err != nil {
		t.Fatalf("OpenStoreAt: %v", err)
	}
	defer s2.Close()
	if v, _ := s2.Get("k"); string(v) != "snap" {
		t.Fatalf("k = %q, want snap", v)
	}
	if v, ok := s2.Get("k2"); !ok || string(v) != "after" {
		t.Fatalf("k2 = %q, %v; want after", v, ok)
	}
	if s2.AppliedIndex() != 3 {
		t.Fatalf("AppliedIndex = %d, want 3", s2.AppliedIndex())
	}
}

func TestStoreCompactedTruncatesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kvs.wal")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := uint64(1); i <= 5; i++ {
		if _, err := s.Apply(i, Command{Op: OpIncr, Key: "seq"}); err != nil {
			t.Fatal(err)
		}
	}
	if s.WALSize() == 0 {
		t.Fatal("WAL should not be empty before compaction")
	}
	if err := s.Compacted(5); err != nil {
		t.Fatalf("Compacted: %v", err)
	}
	if s.WALSize() != 0 {
		t.Fatalf("WALSize = %d, want 0", s.WALSize())
	}
	// 切り詰め後も書き込みできる
	if _, err := s.Apply(6, Command{Op: OpSet, Key: "x", Value: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	if s.WALSize() == 0 {
		t.Fatal("WAL should grow after new writes")
	}
}

func TestStoreApplySkipsDuplicateIndex(t *testing.T) {
	s := NewStore(nil)
	if _, err := s.Apply(1, Command{Op: OpSet, Key: "k", Value: []byte("v1")}); err != nil {
		t.Fatal(err)
	}
	// 同じインデックスの再適用は無視される（WAL 再生との重複防止）
	if _, err := s.Apply(1, Command{Op: OpSet, Key: "k", Value: []byte("v2")}); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("k"); string(v) != "v1" {
		t.Fatalf("k = %q, want v1", v)
	}
	if s.AppliedIndex() != 1 {
		t.Fatalf("AppliedIndex = %d, want 1", s.AppliedIndex())
	}
}
