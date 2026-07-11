package kvs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/noda/linkraft/internal/raft"
	"github.com/noda/linkraft/proto/kvspb"
)

// ErrNoLeader はリトライしてもリーダーに到達できなかった場合に返る。
var ErrNoLeader = errors.New("kvs: no leader available")

// Client は API サーバーから KVS クラスタへアクセスするクライアント。
// リーダー以外に当たった場合はレスポンスのヒントを頼りにリーダーへ追従する。
type Client struct {
	mu     sync.Mutex
	addrs  []string // 既知のノードアドレス
	leader string   // 最後に成功したリーダーのアドレス（空なら不明）
	conns  map[string]*grpc.ClientConn
}

// NewClient はノードアドレスの一覧からクライアントを作る。
func NewClient(addrs []string) *Client {
	return &Client{addrs: addrs, conns: make(map[string]*grpc.ClientConn)}
}

// Close は全接続を閉じる。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	for addr, conn := range c.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(c.conns, addr)
	}
	return firstErr
}

func (c *Client) clientFor(addr string) (kvspb.KVSClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn, ok := c.conns[addr]; ok {
		return kvspb.NewKVSClient(conn), nil
	}
	conn, err := grpc.NewClient(addr, raft.DialOptions()...)
	if err != nil {
		return nil, fmt.Errorf("dial kvs node %s: %w", addr, err)
	}
	c.conns[addr] = conn
	return kvspb.NewKVSClient(conn), nil
}

// candidates はリーダー（既知なら）を先頭にした試行順のアドレス一覧を返す。
func (c *Client) candidates() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.addrs)+1)
	if c.leader != "" {
		out = append(out, c.leader)
	}
	for _, a := range c.addrs {
		if a != c.leader {
			out = append(out, a)
		}
	}
	return out
}

func (c *Client) setLeader(addr string) {
	c.mu.Lock()
	c.leader = addr
	c.mu.Unlock()
}

// call はリーダー追従つきで fn を実行する。fn は (notLeader, leaderHint, err) を返す。
// リーダー選出中は少し待って再試行する。
func (c *Client) call(ctx context.Context, fn func(cli kvspb.KVSClient) (bool, string, error)) error {
	var lastErr error = ErrNoLeader
	preferred := "" // 直近のリーダーヒント

	for attempt := 0; attempt < 10; attempt++ {
		cands := c.candidates()
		if preferred != "" {
			ordered := []string{preferred}
			for _, a := range cands {
				if a != preferred {
					ordered = append(ordered, a)
				}
			}
			cands = ordered
		}

		gotHint := false
		for _, addr := range cands {
			cli, err := c.clientFor(addr)
			if err != nil {
				lastErr = err
				continue
			}
			notLeader, hint, err := fn(cli)
			if err != nil {
				lastErr = fmt.Errorf("kvs node %s: %w", addr, err)
				continue
			}
			if notLeader {
				lastErr = ErrNoLeader
				if hint != "" && hint != addr {
					preferred = hint
					gotHint = true
					break // ヒント先を先頭にして即再試行
				}
				continue
			}
			c.setLeader(addr)
			return nil
		}

		if !gotHint {
			// リーダー不在（選出中など）。少し待ってから再試行する
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return lastErr
}

// Get はキーの値を取得する。
func (c *Client) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	err = c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := cli.Get(ctx, &kvspb.GetRequest{Key: key})
		if err != nil {
			return false, "", err
		}
		if resp.NotLeader {
			return true, resp.LeaderAddr, nil
		}
		value, found = resp.Value, resp.Found
		return false, "", nil
	})
	return value, found, err
}

// Set はキーに値を保存する。
func (c *Client) Set(ctx context.Context, key string, value []byte) error {
	return c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := cli.Set(ctx, &kvspb.SetRequest{Key: key, Value: value})
		if err != nil {
			return false, "", err
		}
		return resp.NotLeader, resp.LeaderAddr, nil
	})
}

// Delete はキーを削除する。
func (c *Client) Delete(ctx context.Context, key string) error {
	return c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := cli.Delete(ctx, &kvspb.DeleteRequest{Key: key})
		if err != nil {
			return false, "", err
		}
		return resp.NotLeader, resp.LeaderAddr, nil
	})
}

// Incr はキーの整数値をインクリメントし、増加後の値を返す。
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	var value int64
	err := c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := cli.Incr(ctx, &kvspb.IncrRequest{Key: key})
		if err != nil {
			return false, "", err
		}
		if resp.NotLeader {
			return true, resp.LeaderAddr, nil
		}
		value = resp.Value
		return false, "", nil
	})
	return value, err
}

// Keys は prefix にマッチするキー一覧を返す。
func (c *Client) Keys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	err := c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := cli.Keys(ctx, &kvspb.KeysRequest{Prefix: prefix})
		if err != nil {
			return false, "", err
		}
		if resp.NotLeader {
			return true, resp.LeaderAddr, nil
		}
		keys = resp.Keys
		return false, "", nil
	})
	return keys, err
}

// AddMember はノードをクラスタに追加する（リーダーに転送される）。
// 追加するノードは先に -join モードで起動しておくこと。
func (c *Client) AddMember(ctx context.Context, id, addr string) error {
	return c.memberChange(ctx, func(cli kvspb.KVSClient) (*kvspb.MemberChangeResponse, error) {
		return cli.AddMember(ctx, &kvspb.AddMemberRequest{Id: id, Addr: addr})
	})
}

// RemoveMember はノードをクラスタから削除する（リーダーに転送される）。
func (c *Client) RemoveMember(ctx context.Context, id string) error {
	return c.memberChange(ctx, func(cli kvspb.KVSClient) (*kvspb.MemberChangeResponse, error) {
		return cli.RemoveMember(ctx, &kvspb.RemoveMemberRequest{Id: id})
	})
}

func (c *Client) memberChange(ctx context.Context, fn func(cli kvspb.KVSClient) (*kvspb.MemberChangeResponse, error)) error {
	var opErr error
	err := c.call(ctx, func(cli kvspb.KVSClient) (bool, string, error) {
		resp, err := fn(cli)
		if err != nil {
			return false, "", err
		}
		if resp.NotLeader {
			return true, resp.LeaderAddr, nil
		}
		if resp.Error != "" {
			opErr = errors.New(resp.Error)
		}
		return false, "", nil
	})
	if err != nil {
		return err
	}
	return opErr
}

// MemberStatus はノードが認識しているクラスタメンバー 1 台分の情報。
type MemberStatus struct {
	ID         string `json:"id"`
	Addr       string `json:"addr"`
	MatchIndex uint64 `json:"match_index"` // リーダー応答時のみ有効
}

// NodeStatus は 1 ノードの状態。
type NodeStatus struct {
	Address       string         `json:"address"`
	NodeID        string         `json:"id"`
	State         string         `json:"state"` // leader / follower / candidate / down
	Term          uint64         `json:"term"`
	LeaderID      string         `json:"leader_id"`
	CommitIndex   uint64         `json:"commit_index"`
	AppliedIndex  uint64         `json:"applied_index"`
	LastLogIndex  uint64         `json:"last_log_index"`
	SnapshotIndex uint64         `json:"snapshot_index"`
	KeysTotal     int64          `json:"keys_total"`
	Members       []MemberStatus `json:"members,omitempty"`
}

func (c *Client) statusOf(ctx context.Context, addr string) NodeStatus {
	st := NodeStatus{Address: addr, State: "down"}
	cli, err := c.clientFor(addr)
	if err != nil {
		return st
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := cli.Status(cctx, &kvspb.StatusRequest{})
	if err != nil {
		return st
	}
	members := make([]MemberStatus, 0, len(resp.Members))
	for _, m := range resp.Members {
		members = append(members, MemberStatus{ID: m.Id, Addr: m.Addr, MatchIndex: m.MatchIndex})
	}
	return NodeStatus{
		Address:       addr,
		NodeID:        resp.NodeId,
		State:         resp.State,
		Term:          resp.Term,
		LeaderID:      resp.LeaderId,
		CommitIndex:   resp.CommitIndex,
		AppliedIndex:  resp.AppliedIndex,
		LastLogIndex:  resp.LastLogIndex,
		SnapshotIndex: resp.SnapshotIndex,
		KeysTotal:     resp.KeysTotal,
		Members:       members,
	}
}

// ClusterStatus は全ノードに Status を問い合わせる。落ちているノードは state="down"。
// 応答に含まれるメンバー情報から、起動時に知らなかったノード（動的に追加された
// ノード）も発見して問い合わせ、以降の書き込みのフェイルオーバー先にも加える。
func (c *Client) ClusterStatus(ctx context.Context) []NodeStatus {
	c.mu.Lock()
	addrs := make([]string, len(c.addrs))
	copy(addrs, c.addrs)
	c.mu.Unlock()

	queried := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		queried[a] = true
	}

	out := make([]NodeStatus, len(addrs))
	var wg sync.WaitGroup
	for i, addr := range addrs {
		wg.Add(1)
		go func(i int, addr string) {
			defer wg.Done()
			out[i] = c.statusOf(ctx, addr)
		}(i, addr)
	}
	wg.Wait()

	// 応答から新メンバーのアドレスを発見して 1 段だけ追加照会する
	extra := []string{}
	for _, st := range out {
		for _, m := range st.Members {
			if m.Addr != "" && !queried[m.Addr] {
				queried[m.Addr] = true
				extra = append(extra, m.Addr)
			}
		}
	}
	if len(extra) > 0 {
		more := make([]NodeStatus, len(extra))
		for i, addr := range extra {
			wg.Add(1)
			go func(i int, addr string) {
				defer wg.Done()
				more[i] = c.statusOf(ctx, addr)
			}(i, addr)
		}
		wg.Wait()
		out = append(out, more...)

		c.mu.Lock()
		known := make(map[string]bool, len(c.addrs))
		for _, a := range c.addrs {
			known[a] = true
		}
		for _, a := range extra {
			if !known[a] {
				c.addrs = append(c.addrs, a)
			}
		}
		c.mu.Unlock()
	}
	return out
}
