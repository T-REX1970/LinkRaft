package raft

import (
	"context"

	"github.com/noda/linkraft/proto/raftpb"
)

// ReadIndex による線形化可能（linearizable）な読み取り（論文 §6.4）。
//
// リーダーであっても、ネットワーク分断で既に新リーダーが選ばれていることに
// 気づいていない可能性があるため、そのまま読むと古い値を返しうる。
// ReadIndex は次の 3 段階で読み取りの線形化可能性を保証する:
//
//  1. 現 term のエントリ（就任時の no-op）がコミット済みであることを保証する。
//     これで commitIndex が「クラスタ全体で確定した最新のコミット位置」になる。
//  2. 空の AppendEntries を全ピアに送り、過半数がまだ自分を term の
//     リーダーと認めていることを確認する（自分が孤立していないことの証明）。
//  3. 手順 1〜2 の時点の commitIndex（= readIndex）がステートマシンに
//     適用されるまで待つ。以降にステートマシンを読めば線形化可能になる。

// applyWaiter は lastApplied が index に達するのを待つウェイター。
type applyWaiter struct {
	index uint64
	ch    chan struct{}
}

// ReadIndex は線形化可能な読み取りのための同期点を作る。
// 成功して返った後にステートマシンを読むと、その読み取りは
// ReadIndex 呼び出し時点までに完了した全ての書き込みを反映していることが保証される。
// リーダーでない場合は NotLeaderError を返す。
func (n *Node) ReadIndex(ctx context.Context) error {
	n.mu.Lock()
	if n.state != Leader {
		err := &NotLeaderError{LeaderID: n.leaderID, LeaderAddr: n.leaderAddrLocked()}
		n.mu.Unlock()
		return err
	}
	term := n.currentTerm
	readIndex := n.commitIndex
	// リーダー就任直後は前 term までのコミット位置を確定できていないため、
	// 少なくとも現 term の no-op エントリの適用を待つ
	if n.termStartIndex > readIndex {
		readIndex = n.termStartIndex
	}
	single := n.clusterSize() == 1
	n.mu.Unlock()

	if !single && !n.confirmLeadership(ctx, term) {
		n.mu.Lock()
		err := &NotLeaderError{LeaderID: n.leaderID, LeaderAddr: n.leaderAddrLocked()}
		n.mu.Unlock()
		return err
	}
	return n.waitApplied(ctx, readIndex)
}

// confirmLeadership は空の AppendEntries（ハートビート）を全ピアに送り、
// 過半数（自分を含む）が term のリーダーとして自分を認めていることを確認する。
func (n *Node) confirmLeadership(ctx context.Context, term uint64) bool {
	n.mu.Lock()
	if n.state != Leader || n.currentTerm != term {
		n.mu.Unlock()
		return false
	}
	peerIDs := make([]string, 0, len(n.peers))
	for id := range n.peers {
		peerIDs = append(peerIDs, id)
	}
	need := n.majority() - 1 // 自分の分を除いた必要 ack 数
	timeout := n.heartbeatInterval * 4
	n.mu.Unlock()
	if need <= 0 {
		return true
	}

	// PrevLogIndex = 0 の空 AppendEntries はログの整合性チェックも
	// commitIndex の前進もせず、「この term のリーダーを認めるか」だけを確かめる
	req := &raftpb.AppendEntriesRequest{Term: term, LeaderId: n.id}
	acks := make(chan bool, len(peerIDs))
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, peerID := range peerIDs {
		go func(peerID string) {
			resp, err := n.trans.AppendEntries(cctx, peerID, req)
			if err != nil {
				acks <- false
				return
			}
			if resp.Term > term {
				n.mu.Lock()
				if resp.Term > n.currentTerm {
					n.becomeFollowerLocked(resp.Term)
				}
				n.mu.Unlock()
				acks <- false
				return
			}
			acks <- true
		}(peerID)
	}
	got := 0
	for range peerIDs {
		select {
		case ok := <-acks:
			if ok {
				got++
				if got >= need {
					return true
				}
			}
		case <-cctx.Done():
			return false
		}
	}
	return false
}

// waitApplied は lastApplied が index に達するまで待つ。
func (n *Node) waitApplied(ctx context.Context, index uint64) error {
	n.mu.Lock()
	if n.lastApplied >= index {
		n.mu.Unlock()
		return nil
	}
	w := applyWaiter{index: index, ch: make(chan struct{})}
	n.applyWaiters = append(n.applyWaiters, w)
	n.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-n.stopCh:
		return ErrShutdown
	}
}

// notifyApplyWaitersLocked は lastApplied の前進を待っているウェイターを起こす。
func (n *Node) notifyApplyWaitersLocked() {
	if len(n.applyWaiters) == 0 {
		return
	}
	kept := n.applyWaiters[:0]
	for _, w := range n.applyWaiters {
		if w.index <= n.lastApplied {
			close(w.ch)
		} else {
			kept = append(kept, w)
		}
	}
	n.applyWaiters = kept
}
