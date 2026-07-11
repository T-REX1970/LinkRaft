package raft

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/noda/linkraft/proto/raftpb"
)

// TestPreVotePreventsTermInflation は、受信できなくなったフォロワーが
// pre-vote のおかげで term を上げず、クラスタを乱さないことを検証する。
// （pre-vote がないと、孤立ノードが選挙のたびに term を上げ、復帰時や
// 一方向断のときに健全なリーダーを退任させてしまう。）
func TestPreVotePreventsTermInflation(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)
	leaderTerm := nodes[leader].node.GetStatus().Term

	// フォロワーへの受信を止める（送信はできる = 一方向断）
	follower := (leader + 1) % len(nodes)
	nodes[follower].server.Stop()
	followerTerm := nodes[follower].node.GetStatus().Term

	// 選挙タイムアウトを何回も超える時間だけ待つ
	time.Sleep(1200 * time.Millisecond)

	// リーダーは変わらず、term も上がっていない
	st := nodes[leader].node.GetStatus()
	if st.State != Leader {
		t.Fatalf("leader lost leadership: %+v", st)
	}
	if st.Term != leaderTerm {
		t.Fatalf("leader term changed %d -> %d (pre-vote should prevent this)", leaderTerm, st.Term)
	}
	// 孤立フォロワーも pre-vote が拒否され続けるので term を上げない
	fst := nodes[follower].node.GetStatus()
	if fst.Term != followerTerm {
		t.Fatalf("isolated follower inflated term %d -> %d", followerTerm, fst.Term)
	}
}

func TestReadIndex(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := nodes[leader].node.Propose(ctx, []byte("v1")); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// リーダーでの ReadIndex は成功し、適用済みであることを保証する
	if err := nodes[leader].node.ReadIndex(ctx); err != nil {
		t.Fatalf("ReadIndex on leader: %v", err)
	}
	st := nodes[leader].node.GetStatus()
	if st.AppliedIndex < st.CommitIndex {
		t.Fatalf("after ReadIndex applied=%d < commit=%d", st.AppliedIndex, st.CommitIndex)
	}
	if got := nodes[leader].applier.commands(); len(got) != 1 || string(got[0]) != "v1" {
		t.Fatalf("state machine = %q, want [v1]", got)
	}

	// フォロワーでは NotLeaderError
	follower := (leader + 1) % len(nodes)
	err := nodes[follower].node.ReadIndex(ctx)
	var nl *NotLeaderError
	if !errors.As(err, &nl) {
		t.Fatalf("ReadIndex on follower = %v, want *NotLeaderError", err)
	}
}

// TestMembershipChange はノードの動的追加と削除を検証する。
func TestMembershipChange(t *testing.T) {
	nodes := newTestCluster(t, 3, 0)
	leader := waitForLeader(t, nodes, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := nodes[leader].node.Propose(ctx, []byte("before-add")); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// ---- 追加: 4 台目を join モードで起動して AddMember ----
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr3 := ln.Addr().String()
	peers := map[string]string{}
	for i, tn := range nodes {
		peers[fmt.Sprintf("node-%d", i)] = tn.addr
	}
	applier := &testApplier{}
	newNode, err := NewNode(Config{
		ID:                 "node-3",
		Addr:               addr3,
		Peers:              peers,
		Join:               true,
		DataDir:            t.TempDir(),
		ElectionTimeoutMin: 150 * time.Millisecond,
		ElectionTimeoutMax: 300 * time.Millisecond,
		HeartbeatInterval:  30 * time.Millisecond,
		Apply:              applier.apply,
	})
	if err != nil {
		t.Fatalf("NewNode(node-3): %v", err)
	}
	srv := grpc.NewServer()
	raftpb.RegisterRaftServer(srv, NewServer(newNode))
	go srv.Serve(ln)
	newNode.Start()
	t.Cleanup(func() { newNode.Stop(); srv.Stop() })

	if err := nodes[leader].node.AddMember(ctx, "node-3", addr3); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if got := nodes[leader].node.GetStatus().Peers; got != 4 {
		t.Fatalf("cluster size after add = %d, want 4", got)
	}

	// 追加後の書き込みが新ノードにも複製・適用される
	if _, err := nodes[leader].node.Propose(ctx, []byte("after-add")); err != nil {
		t.Fatalf("Propose after add: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cmds := applier.commands()
		if len(cmds) == 2 && string(cmds[1]) == "after-add" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cmds := applier.commands(); len(cmds) != 2 {
		t.Fatalf("new node applied %d commands, want 2", len(cmds))
	}

	// リーダー自身の削除は拒否される
	leaderID := nodes[leader].node.GetStatus().ID
	if err := nodes[leader].node.RemoveMember(ctx, leaderID); !errors.Is(err, ErrRemoveLeader) {
		t.Fatalf("RemoveMember(leader) = %v, want ErrRemoveLeader", err)
	}

	// ---- 削除: 既存フォロワーを 1 台外す ----
	victim := (leader + 1) % 3
	victimID := fmt.Sprintf("node-%d", victim)
	if err := nodes[leader].node.RemoveMember(ctx, victimID); err != nil {
		t.Fatalf("RemoveMember(%s): %v", victimID, err)
	}
	if got := nodes[leader].node.GetStatus().Peers; got != 3 {
		t.Fatalf("cluster size after remove = %d, want 3", got)
	}

	// 削除後も書き込みできる（過半数は残り 3 台の 2 台）
	if _, err := nodes[leader].node.Propose(ctx, []byte("after-remove")); err != nil {
		t.Fatalf("Propose after remove: %v", err)
	}

	// 除去されたノードは選挙を起こさず、リーダーも安定している
	time.Sleep(600 * time.Millisecond)
	if st := nodes[leader].node.GetStatus(); st.State != Leader {
		t.Fatalf("leader destabilized after remove: %+v", st)
	}
}
