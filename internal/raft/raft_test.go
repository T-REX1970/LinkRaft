package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/noda/linkraft/proto/raftpb"
)

// testApplier はテスト用ステートマシン。適用されたコマンドを記録する。
// Snapshotter も実装する（スナップショット = コマンド列全体のシリアライズ）。
type testApplier struct {
	mu   sync.Mutex
	cmds [][]byte
}

func (a *testApplier) apply(_ uint64, cmd []byte) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := make([]byte, len(cmd))
	copy(c, cmd)
	a.cmds = append(a.cmds, c)
	return len(a.cmds), nil
}

func (a *testApplier) commands() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([][]byte, len(a.cmds))
	copy(out, a.cmds)
	return out
}

func (a *testApplier) Snapshot() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return json.Marshal(a.cmds)
}

func (a *testApplier) Restore(_ uint64, data []byte) error {
	var cmds [][]byte
	if err := json.Unmarshal(data, &cmds); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cmds = cmds
	return nil
}

func (a *testApplier) Compacted(uint64) error { return nil }

type testNode struct {
	node    *Node
	applier *testApplier
	server  *grpc.Server
	addr    string
}

// newTestCluster は localhost 上に size ノードの Raft クラスタを起動する。
// snapThreshold > 0 ならスナップショット（ログコンパクション）を有効にする。
func newTestCluster(t *testing.T, size int, snapThreshold uint64) []*testNode {
	t.Helper()

	listeners := make([]net.Listener, size)
	addrs := make([]string, size)
	for i := range listeners {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		listeners[i] = ln
		addrs[i] = ln.Addr().String()
	}

	nodes := make([]*testNode, size)
	for i := range nodes {
		id := fmt.Sprintf("node-%d", i)
		peers := make(map[string]string)
		for j := range addrs {
			if j != i {
				peers[fmt.Sprintf("node-%d", j)] = addrs[j]
			}
		}
		applier := &testApplier{}
		cfg := Config{
			ID:      id,
			Addr:    addrs[i],
			Peers:   peers,
			DataDir: t.TempDir(),
			// テスト高速化
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  30 * time.Millisecond,
			Apply:              applier.apply,
		}
		if snapThreshold > 0 {
			cfg.Snapshotter = applier
			cfg.SnapshotThreshold = snapThreshold
		}
		node, err := NewNode(cfg)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		srv := grpc.NewServer()
		raftpb.RegisterRaftServer(srv, NewServer(node))
		go srv.Serve(listeners[i])
		node.Start()
		nodes[i] = &testNode{node: node, applier: applier, server: srv, addr: addrs[i]}
	}

	t.Cleanup(func() {
		for _, tn := range nodes {
			tn.node.Stop()
			tn.server.Stop()
		}
	})
	return nodes
}

// waitForLeader は alive なノードの中からリーダーが 1 つ選出されるまで待つ。
func waitForLeader(t *testing.T, nodes []*testNode, alive map[int]bool) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		leaders := []int{}
		for i, tn := range nodes {
			if alive != nil && !alive[i] {
				continue
			}
			if tn.node.GetStatus().State == Leader {
				leaders = append(leaders, i)
			}
		}
		if len(leaders) == 1 {
			return leaders[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no single leader elected within deadline")
	return -1
}

func TestLeaderElection(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)
	st := nodes[leader].node.GetStatus()
	if st.Term == 0 {
		t.Fatalf("leader term should be > 0, got %d", st.Term)
	}
	// フォロワーもリーダーを認識する
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, tn := range nodes {
			if tn.node.GetStatus().LeaderID != st.ID {
				ok = false
			}
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("followers did not learn the leader")
}

func TestLogReplication(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)

	want := [][]byte{}
	for i := 0; i < 5; i++ {
		cmd := []byte(fmt.Sprintf("cmd-%d", i))
		want = append(want, cmd)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := nodes[leader].node.Propose(ctx, cmd)
		cancel()
		if err != nil {
			t.Fatalf("Propose(%d): %v", i, err)
		}
		if res.(int) != i+1 {
			t.Fatalf("apply result = %v, want %d", res, i+1)
		}
	}

	// 全ノードに複製・適用されるまで待つ
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for _, tn := range nodes {
			if len(tn.applier.commands()) == len(want) {
				done++
			}
		}
		if done == len(nodes) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, tn := range nodes {
		got := tn.applier.commands()
		if len(got) != len(want) {
			t.Fatalf("node %d applied %d commands, want %d", i, len(got), len(want))
		}
		for j := range want {
			if string(got[j]) != string(want[j]) {
				t.Fatalf("node %d cmd %d = %q, want %q", i, j, got[j], want[j])
			}
		}
	}
}

func TestProposeOnFollowerReturnsNotLeader(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)

	follower := (leader + 1) % len(nodes)

	// フォロワーがハートビートでリーダーを学習するまで待つ
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[follower].node.GetStatus().LeaderAddr == nodes[leader].addr {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := nodes[follower].node.Propose(ctx, []byte("x"))
	nlErr, ok := err.(*NotLeaderError)
	if !ok {
		t.Fatalf("err = %v, want *NotLeaderError", err)
	}
	if nlErr.LeaderAddr != nodes[leader].addr {
		t.Fatalf("leader hint = %q, want %q", nlErr.LeaderAddr, nodes[leader].addr)
	}
}

func TestLeaderFailover(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)

	// コミット済みデータを作っておく
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err := nodes[leader].node.Propose(ctx, []byte("before-failover"))
	cancel()
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// リーダーを落とす
	nodes[leader].server.Stop()
	nodes[leader].node.Stop()

	alive := map[int]bool{}
	for i := range nodes {
		alive[i] = i != leader
	}
	newLeader := waitForLeader(t, nodes, alive)
	if newLeader == leader {
		t.Fatal("old leader should not be leader")
	}

	// 新リーダーで書き込みできる
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if _, err := nodes[newLeader].node.Propose(ctx2, []byte("after-failover")); err != nil {
		t.Fatalf("Propose after failover: %v", err)
	}
}

// TestSnapshotCatchUp は、リーダーがログをコンパクションした後に復帰した
// フォロワーが InstallSnapshot 経由で追いつけることを検証する。
func TestSnapshotCatchUp(t *testing.T) {
	nodes := newTestCluster(t, 3, 5) // 5 エントリごとにスナップショット
	leader := waitForLeader(t, nodes, nil)

	// フォロワーを 1 台切り離す（プロセスは生きているが RPC が届かない）。
	// pre-vote 未実装のため、切断中に選挙を起こしてリーダーを乱さないよう
	// 選挙タイマーを事実上止めておく。
	follower := (leader + 1) % len(nodes)
	fn := nodes[follower].node
	fn.mu.Lock()
	fn.electionTimeoutMin = time.Hour
	fn.electionTimeoutMax = 2 * time.Hour
	fn.electionTimeout = time.Hour
	fn.mu.Unlock()
	nodes[follower].server.Stop()

	// スナップショットのしきい値を大きく超える数を書き込む
	want := [][]byte{}
	for i := 0; i < 20; i++ {
		cmd := []byte(fmt.Sprintf("cmd-%d", i))
		want = append(want, cmd)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := nodes[leader].node.Propose(ctx, cmd)
		cancel()
		if err != nil {
			t.Fatalf("Propose(%d): %v", i, err)
		}
	}

	// リーダーがログをコンパクションするまで待つ
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[leader].node.GetStatus().SnapshotIndex > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	leaderSt := nodes[leader].node.GetStatus()
	if leaderSt.SnapshotIndex == 0 {
		t.Fatal("leader did not take a snapshot")
	}

	// フォロワーを復帰させる（同じアドレスで gRPC サーバーを立て直す）
	ln, err := net.Listen("tcp", nodes[follower].addr)
	if err != nil {
		t.Fatalf("relisten: %v", err)
	}
	srv := grpc.NewServer()
	raftpb.RegisterRaftServer(srv, NewServer(nodes[follower].node))
	go srv.Serve(ln)
	t.Cleanup(srv.Stop)

	// スナップショット + 差分ログで追いつくまで待つ
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := nodes[follower].node.GetStatus()
		if st.AppliedIndex >= leaderSt.AppliedIndex && st.SnapshotIndex > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := nodes[follower].node.GetStatus()
	if st.SnapshotIndex == 0 {
		t.Fatalf("follower did not install a snapshot: %+v", st)
	}
	if st.AppliedIndex < leaderSt.AppliedIndex {
		t.Fatalf("follower applied=%d, want >= %d", st.AppliedIndex, leaderSt.AppliedIndex)
	}

	// ステートマシンの内容がリーダーと一致する
	got := nodes[follower].applier.commands()
	if len(got) != len(want) {
		t.Fatalf("follower has %d commands, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != string(want[i]) {
			t.Fatalf("cmd %d = %q, want %q", i, got[i], want[i])
		}
	}

	// 復帰後は通常のログ複製に戻る
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := nodes[leader].node.Propose(ctx, []byte("after-catchup")); err != nil {
		t.Fatalf("Propose after catch-up: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cmds := nodes[follower].applier.commands()
		if len(cmds) == len(want)+1 && string(cmds[len(cmds)-1]) == "after-catchup" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("follower did not receive entries after catch-up")
}
