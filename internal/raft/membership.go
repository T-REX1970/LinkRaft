package raft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// メンバーシップ変更（動的なノードの追加・削除）。
//
// 一度に 1 ノードずつ変更する単一サーバー方式（論文 §4.1 の簡易版）を採る。
// 1 ノードずつなら変更前後の構成の過半数が必ず重なるため、joint consensus を
// 使わなくても「同じ term に 2 人のリーダー」が生まれないことが保証される。
//
// 設定変更は全メンバー（自分を含む）の id -> addr マップを JSON にした
// EntryConfig エントリとして通常のログ複製で配り、各ノードはコミットを待たず
// ログに追記した時点で新しい構成に切り替える。

// ErrConfigChangeInProgress は前の設定変更がコミットされる前に
// 次の変更を要求した場合に返る（単一サーバー方式の前提を守るため）。
var ErrConfigChangeInProgress = errors.New("raft: another configuration change is in progress")

// ErrRemoveLeader はリーダー自身の削除を要求した場合に返る。
// リーダーの削除はリーダーを移してから行う（学習用実装の簡略化）。
var ErrRemoveLeader = errors.New("raft: cannot remove the current leader; remove a follower or wait for a leadership change")

func copyMembers(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for id, addr := range m {
		out[id] = addr
	}
	return out
}

// peersOf は members から自分を除いたピア表を返す。
func peersOf(members map[string]string, self string) map[string]string {
	out := make(map[string]string, len(members))
	for id, addr := range members {
		if id != self {
			out[id] = addr
		}
	}
	return out
}

func encodeMembers(m map[string]string) ([]byte, error) {
	return json.Marshal(m)
}

func decodeMembers(b []byte) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func memberIDs(m map[string]string) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AddMember はノードをクラスタに追加する（リーダーのみ）。
// 追加するノードは先に -join モードで起動しておくこと。
func (n *Node) AddMember(ctx context.Context, id, addr string) error {
	if id == "" || addr == "" {
		return errors.New("raft: member id and addr are required")
	}
	return n.proposeConfig(ctx, func(members map[string]string) error {
		if cur, ok := members[id]; ok {
			return fmt.Errorf("raft: %s is already a member (addr=%s)", id, cur)
		}
		members[id] = addr
		return nil
	})
}

// RemoveMember はノードをクラスタから削除する（リーダーのみ）。
func (n *Node) RemoveMember(ctx context.Context, id string) error {
	return n.proposeConfig(ctx, func(members map[string]string) error {
		if id == n.id {
			return ErrRemoveLeader
		}
		if _, ok := members[id]; !ok {
			return fmt.Errorf("raft: %s is not a member", id)
		}
		delete(members, id)
		return nil
	})
}

// proposeConfig は現在の構成に mutate を適用した新構成を EntryConfig として
// ログに追加し、コミットされるまで待つ。構成そのものは追記した時点で有効になる。
func (n *Node) proposeConfig(ctx context.Context, mutate func(map[string]string) error) error {
	n.mu.Lock()
	if n.state != Leader {
		err := &NotLeaderError{LeaderID: n.leaderID, LeaderAddr: n.leaderAddrLocked()}
		n.mu.Unlock()
		return err
	}
	// 前の変更が未コミットのうちは次を受け付けない（一度に 1 変更まで）
	if n.configIndex > n.commitIndex {
		n.mu.Unlock()
		return ErrConfigChangeInProgress
	}
	members := copyMembers(n.members)
	if err := mutate(members); err != nil {
		n.mu.Unlock()
		return err
	}
	cmd, err := encodeMembers(members)
	if err != nil {
		n.mu.Unlock()
		return fmt.Errorf("encode members: %w", err)
	}
	index := n.log.LastIndex() + 1
	entry := Entry{Term: n.currentTerm, Index: index, Command: cmd, Type: EntryConfig}
	if err := n.log.Append(entry); err != nil {
		n.mu.Unlock()
		return fmt.Errorf("append config entry: %w", err)
	}
	ch := make(chan applyResult, 1)
	n.waiters[index] = ch
	n.applyConfigLocked(members, index)
	n.advanceCommitLocked()
	n.broadcastAppendLocked()
	n.mu.Unlock()

	select {
	case r := <-ch:
		return r.err
	case <-ctx.Done():
		n.mu.Lock()
		delete(n.waiters, index)
		n.mu.Unlock()
		return ctx.Err()
	case <-n.stopCh:
		return ErrShutdown
	}
}

// applyConfigLocked は新しいクラスタ構成に切り替える。
// リーダーは新ピアの複製状態を初期化し、除去されたピアの状態を破棄する。
func (n *Node) applyConfigLocked(members map[string]string, index uint64) {
	n.members = copyMembers(members)
	n.configIndex = index
	n.peers = peersOf(members, n.id)
	if pu, ok := n.trans.(PeerUpdater); ok {
		pu.UpdatePeers(n.peers)
	}
	if n.state == Leader {
		for id := range n.peers {
			if _, ok := n.nextIndex[id]; !ok {
				n.nextIndex[id] = n.log.LastIndex() + 1
				n.matchIndex[id] = 0
			}
		}
		for id := range n.nextIndex {
			if _, ok := n.peers[id]; !ok {
				delete(n.nextIndex, id)
				delete(n.matchIndex, id)
				delete(n.snapInFlight, id)
			}
		}
	}
	if _, ok := members[n.id]; ok {
		n.joining = false
	}
	n.logger.Info("cluster configuration changed", "config_index", index, "members", memberIDs(members))
}

// recomputeConfigLocked はログの切り捨てで現在の設定変更エントリが消えた場合に、
// 残っているログ → スナップショット → ブートストラップ構成の順で構成を復元する。
func (n *Node) recomputeConfigLocked() {
	if e, ok := n.log.LatestConfig(); ok {
		if m, err := decodeMembers(e.Command); err == nil {
			n.applyConfigLocked(m, e.Index)
			return
		}
		n.logger.Error("failed to decode config entry during recompute", "index", e.Index)
	}
	if len(n.snapMembers) > 0 {
		n.applyConfigLocked(n.snapMembers, n.log.SnapIndex())
		return
	}
	n.applyConfigLocked(n.bootstrapMembers, 0)
}

// maybeApplyConfigEntryLocked は handleAppendEntries が新しいエントリを
// 受け入れたときに呼ばれ、設定変更エントリなら即座に構成へ反映する。
func (n *Node) maybeApplyConfigEntryLocked(e Entry) {
	if e.Type != EntryConfig {
		return
	}
	m, err := decodeMembers(e.Command)
	if err != nil {
		n.logger.Error("failed to decode config entry", "index", e.Index, "err", err)
		return
	}
	n.applyConfigLocked(m, e.Index)
}

// Members は現在のクラスタ構成（自分を含む id -> addr）を返す。
func (n *Node) Members() map[string]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return copyMembers(n.members)
}
