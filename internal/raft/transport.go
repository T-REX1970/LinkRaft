package raft

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/noda/linkraft/proto/raftpb"
)

// Transport はピアへの Raft RPC 送信を抽象化する。
type Transport interface {
	RequestVote(ctx context.Context, peerID string, req *raftpb.RequestVoteRequest) (*raftpb.RequestVoteResponse, error)
	AppendEntries(ctx context.Context, peerID string, req *raftpb.AppendEntriesRequest) (*raftpb.AppendEntriesResponse, error)
	InstallSnapshot(ctx context.Context, peerID string, req *raftpb.InstallSnapshotRequest) (*raftpb.InstallSnapshotResponse, error)
	Close() error
}

// GRPCTransport は gRPC ベースの Transport 実装。
// 接続は遅延生成され、以降キャッシュされる。
type GRPCTransport struct {
	mu    sync.Mutex
	addrs map[string]string // peerID -> address
	conns map[string]*grpc.ClientConn
}

// NewGRPCTransport はピアのアドレス表から Transport を作る。
func NewGRPCTransport(peerAddrs map[string]string) *GRPCTransport {
	addrs := make(map[string]string, len(peerAddrs))
	for id, a := range peerAddrs {
		addrs[id] = a
	}
	return &GRPCTransport{addrs: addrs, conns: make(map[string]*grpc.ClientConn)}
}

func (t *GRPCTransport) client(peerID string) (raftpb.RaftClient, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if conn, ok := t.conns[peerID]; ok {
		return raftpb.NewRaftClient(conn), nil
	}
	addr, ok := t.addrs[peerID]
	if !ok {
		return nil, fmt.Errorf("unknown peer: %s", peerID)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial peer %s: %w", peerID, err)
	}
	t.conns[peerID] = conn
	return raftpb.NewRaftClient(conn), nil
}

// RequestVote は投票依頼 RPC を送る。
func (t *GRPCTransport) RequestVote(ctx context.Context, peerID string, req *raftpb.RequestVoteRequest) (*raftpb.RequestVoteResponse, error) {
	c, err := t.client(peerID)
	if err != nil {
		return nil, err
	}
	return c.RequestVote(ctx, req)
}

// AppendEntries はログ複製 / ハートビート RPC を送る。
func (t *GRPCTransport) AppendEntries(ctx context.Context, peerID string, req *raftpb.AppendEntriesRequest) (*raftpb.AppendEntriesResponse, error) {
	c, err := t.client(peerID)
	if err != nil {
		return nil, err
	}
	return c.AppendEntries(ctx, req)
}

// InstallSnapshot はスナップショット転送 RPC を送る。
func (t *GRPCTransport) InstallSnapshot(ctx context.Context, peerID string, req *raftpb.InstallSnapshotRequest) (*raftpb.InstallSnapshotResponse, error) {
	c, err := t.client(peerID)
	if err != nil {
		return nil, err
	}
	return c.InstallSnapshot(ctx, req)
}

// Close は全ピアへの接続を閉じる。
func (t *GRPCTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var firstErr error
	for id, conn := range t.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(t.conns, id)
	}
	return firstErr
}

// Server は raftpb.RaftServer の実装。受信 RPC を Node に委譲する。
type Server struct {
	raftpb.UnimplementedRaftServer
	node *Node
}

// NewServer は Node に紐づく gRPC サーバー実装を作る。
func NewServer(n *Node) *Server {
	return &Server{node: n}
}

// RequestVote は投票依頼を処理する。
func (s *Server) RequestVote(_ context.Context, req *raftpb.RequestVoteRequest) (*raftpb.RequestVoteResponse, error) {
	return s.node.handleRequestVote(req), nil
}

// AppendEntries はログ複製 / ハートビートを処理する。
func (s *Server) AppendEntries(_ context.Context, req *raftpb.AppendEntriesRequest) (*raftpb.AppendEntriesResponse, error) {
	return s.node.handleAppendEntries(req), nil
}

// InstallSnapshot はスナップショット転送を処理する。
func (s *Server) InstallSnapshot(_ context.Context, req *raftpb.InstallSnapshotRequest) (*raftpb.InstallSnapshotResponse, error) {
	return s.node.handleInstallSnapshot(req), nil
}
