package kvs

import (
	"context"
	"errors"

	"github.com/noda/linkraft/internal/raft"
	"github.com/noda/linkraft/proto/kvspb"
)

// Server は kvspb.KVSServer の実装。書き込みは Raft 経由で合意してから
// ステートマシン（Store）に適用する。読み取りはリーダーのみ受け付け、
// ReadIndex（過半数への生存確認 + 適用待ち）を挟むことで線形化可能性を保証する。
type Server struct {
	kvspb.UnimplementedKVSServer
	store *Store
	node  *raft.Node
}

// NewServer は Store と Raft ノードを束ねた KVS サーバーを作る。
func NewServer(store *Store, node *raft.Node) *Server {
	return &Server{store: store, node: node}
}

// propose はコマンドを Raft で合意させる。
// リーダーでない場合は notLeader = true とリーダーのヒントを返す。
func (s *Server) propose(ctx context.Context, cmd Command) (res any, notLeader bool, hint string, err error) {
	b, err := EncodeCommand(cmd)
	if err != nil {
		return nil, false, "", err
	}
	res, err = s.node.Propose(ctx, b)
	if err != nil {
		var nl *raft.NotLeaderError
		if errors.As(err, &nl) {
			return nil, true, nl.LeaderAddr, nil
		}
		return nil, false, "", err
	}
	return res, false, "", nil
}

// readIndex は線形化可能な読み取りの前処理。リーダーでない（または
// リーダーの座を失っていた）場合は notLeader = true とヒントを返す。
func (s *Server) readIndex(ctx context.Context) (notLeader bool, hint string, err error) {
	if err := s.node.ReadIndex(ctx); err != nil {
		var nl *raft.NotLeaderError
		if errors.As(err, &nl) {
			return true, nl.LeaderAddr, nil
		}
		return false, "", err
	}
	return false, "", nil
}

// Get はキーの値を返す。
func (s *Server) Get(ctx context.Context, req *kvspb.GetRequest) (*kvspb.GetResponse, error) {
	notLeader, hint, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	if notLeader {
		return &kvspb.GetResponse{NotLeader: true, LeaderAddr: hint}, nil
	}
	v, ok := s.store.Get(req.Key)
	return &kvspb.GetResponse{Value: v, Found: ok}, nil
}

// Set はキーに値を保存する。
func (s *Server) Set(ctx context.Context, req *kvspb.SetRequest) (*kvspb.SetResponse, error) {
	_, notLeader, hint, err := s.propose(ctx, Command{Op: OpSet, Key: req.Key, Value: req.Value})
	if err != nil {
		return nil, err
	}
	if notLeader {
		return &kvspb.SetResponse{NotLeader: true, LeaderAddr: hint}, nil
	}
	return &kvspb.SetResponse{}, nil
}

// Delete はキーを削除する。
func (s *Server) Delete(ctx context.Context, req *kvspb.DeleteRequest) (*kvspb.DeleteResponse, error) {
	_, notLeader, hint, err := s.propose(ctx, Command{Op: OpDelete, Key: req.Key})
	if err != nil {
		return nil, err
	}
	if notLeader {
		return &kvspb.DeleteResponse{NotLeader: true, LeaderAddr: hint}, nil
	}
	return &kvspb.DeleteResponse{}, nil
}

// Incr はキーの整数値をインクリメントする。
func (s *Server) Incr(ctx context.Context, req *kvspb.IncrRequest) (*kvspb.IncrResponse, error) {
	res, notLeader, hint, err := s.propose(ctx, Command{Op: OpIncr, Key: req.Key})
	if err != nil {
		return nil, err
	}
	if notLeader {
		return &kvspb.IncrResponse{NotLeader: true, LeaderAddr: hint}, nil
	}
	n, ok := res.(int64)
	if !ok {
		return nil, errors.New("kvs: unexpected incr result type")
	}
	return &kvspb.IncrResponse{Value: n}, nil
}

// Keys は prefix にマッチするキー一覧を返す。
func (s *Server) Keys(ctx context.Context, req *kvspb.KeysRequest) (*kvspb.KeysResponse, error) {
	notLeader, hint, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	if notLeader {
		return &kvspb.KeysResponse{NotLeader: true, LeaderAddr: hint}, nil
	}
	return &kvspb.KeysResponse{Keys: s.store.Keys(req.Prefix)}, nil
}

// Status はノードの状態を返す（どのノードでも応答する）。
func (s *Server) Status(_ context.Context, _ *kvspb.StatusRequest) (*kvspb.StatusResponse, error) {
	st := s.node.GetStatus()
	members := make([]*kvspb.Member, 0, len(st.Members))
	for _, m := range st.Members {
		members = append(members, &kvspb.Member{Id: m.ID, Addr: m.Addr, MatchIndex: m.MatchIndex})
	}
	return &kvspb.StatusResponse{
		NodeId:        st.ID,
		State:         st.State.String(),
		Term:          st.Term,
		LeaderId:      st.LeaderID,
		CommitIndex:   st.CommitIndex,
		AppliedIndex:  st.AppliedIndex,
		SnapshotIndex: st.SnapshotIndex,
		LastLogIndex:  st.LastLogIndex,
		KeysTotal:     int64(s.store.Len()),
		Members:       members,
	}, nil
}

// memberChangeResponse は raft のメンバーシップ変更結果を RPC 応答に変換する。
func memberChangeResponse(err error) (*kvspb.MemberChangeResponse, error) {
	if err == nil {
		return &kvspb.MemberChangeResponse{}, nil
	}
	var nl *raft.NotLeaderError
	if errors.As(err, &nl) {
		return &kvspb.MemberChangeResponse{NotLeader: true, LeaderAddr: nl.LeaderAddr}, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return &kvspb.MemberChangeResponse{Error: err.Error()}, nil
}

// AddMember はノードをクラスタに追加する。
func (s *Server) AddMember(ctx context.Context, req *kvspb.AddMemberRequest) (*kvspb.MemberChangeResponse, error) {
	return memberChangeResponse(s.node.AddMember(ctx, req.Id, req.Addr))
}

// RemoveMember はノードをクラスタから削除する。
func (s *Server) RemoveMember(ctx context.Context, req *kvspb.RemoveMemberRequest) (*kvspb.MemberChangeResponse, error) {
	return memberChangeResponse(s.node.RemoveMember(ctx, req.Id))
}
