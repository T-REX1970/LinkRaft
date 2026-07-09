// Package raft は Raft 合意アルゴリズムの学習用実装。
// リーダー選出・ログ複製・スナップショット（ログコンパクション）を実装する。
// メンバーシップ変更は未対応。
package raft

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"sync"
	"time"

	"github.com/noda/linkraft/proto/raftpb"
)

// ErrShutdown はノード停止後の操作で返る。
var ErrShutdown = errors.New("raft: node is shut down")

// NotLeaderError はリーダー以外への書き込みで返る。リーダーのヒントを含む。
type NotLeaderError struct {
	LeaderID   string
	LeaderAddr string
}

func (e *NotLeaderError) Error() string {
	return fmt.Sprintf("raft: not leader (leader=%s %s)", e.LeaderID, e.LeaderAddr)
}

// ApplyFunc はコミット済みエントリをステートマシンに適用する。
// 戻り値は Propose の呼び出し元に返される。
type ApplyFunc func(index uint64, command []byte) (any, error)

// Config は Raft ノードの設定。
type Config struct {
	ID      string            // 自ノードの ID（例: "node-0"）
	Addr    string            // 自ノードの広報アドレス（リーダーヒント用）
	Peers   map[string]string // 自分以外のノード: ID -> アドレス
	DataDir string            // meta / ログの永続化先ディレクトリ

	ElectionTimeoutMin time.Duration // デフォルト 300ms
	ElectionTimeoutMax time.Duration // デフォルト 600ms
	HeartbeatInterval  time.Duration // デフォルト 50ms

	Transport Transport // nil なら Peers から GRPCTransport を作る
	Apply     ApplyFunc // 必須
	// ステートマシンが適用済みのインデックス（再起動時の重複適用回避に使う）
	AppliedIndex uint64

	// Snapshotter を与えると、適用済みエントリが SnapshotThreshold を超えるたびに
	// スナップショットを取得してログをコンパクションする。
	Snapshotter       Snapshotter
	SnapshotThreshold uint64 // デフォルト 1000 エントリ

	Logger *slog.Logger
}

type applyResult struct {
	res any
	err error
}

// Node は Raft ノード本体。
type Node struct {
	mu sync.Mutex

	id    string
	addr  string
	peers map[string]string

	state       State
	currentTerm uint64
	votedFor    string
	leaderID    string

	log         *Log
	commitIndex uint64
	lastApplied uint64

	// リーダーのみ使用
	nextIndex  map[string]uint64
	matchIndex map[string]uint64

	electionReset   time.Time
	electionTimeout time.Duration
	lastHeartbeat   time.Time

	heartbeatInterval  time.Duration
	electionTimeoutMin time.Duration
	electionTimeoutMax time.Duration

	trans    Transport
	ownTrans bool
	applyFn  ApplyFunc
	waiters  map[uint64]chan applyResult

	snapshotter       Snapshotter
	snapshotThreshold uint64
	dataDir           string
	snapInFlight      map[string]bool // InstallSnapshot 送信中のピア

	metaPath string
	applyCh  chan struct{}
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	electionsTotal uint64
	rnd            *rand.Rand
	logger         *slog.Logger
}

// NewNode は永続化状態を復元して Raft ノードを作る。Start で稼働を開始する。
func NewNode(cfg Config) (*Node, error) {
	if cfg.Apply == nil {
		return nil, errors.New("raft: Config.Apply is required")
	}
	if cfg.ElectionTimeoutMin == 0 {
		cfg.ElectionTimeoutMin = 300 * time.Millisecond
	}
	if cfg.ElectionTimeoutMax == 0 {
		cfg.ElectionTimeoutMax = 600 * time.Millisecond
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 50 * time.Millisecond
	}
	if cfg.SnapshotThreshold == 0 {
		cfg.SnapshotThreshold = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	metaPath := filepath.Join(cfg.DataDir, "raft-meta.json")
	m, err := loadMeta(metaPath)
	if err != nil {
		return nil, err
	}
	// スナップショットがあればその境界からログを復元する
	var snapIndex, snapTerm uint64
	if snap, err := LoadSnapshot(cfg.DataDir); err != nil {
		return nil, err
	} else if snap != nil {
		snapIndex, snapTerm = snap.Index, snap.Term
	}
	lg, err := OpenLog(filepath.Join(cfg.DataDir, "raft-log.jsonl"), snapIndex, snapTerm)
	if err != nil {
		return nil, err
	}

	trans := cfg.Transport
	ownTrans := false
	if trans == nil {
		trans = NewGRPCTransport(cfg.Peers)
		ownTrans = true
	}

	// スナップショットの方が進んでいる場合はそちらに合わせる
	applied := cfg.AppliedIndex
	if snapIndex > applied {
		applied = snapIndex
	}

	n := &Node{
		id:                 cfg.ID,
		addr:               cfg.Addr,
		peers:              cfg.Peers,
		state:              Follower,
		currentTerm:        m.Term,
		votedFor:           m.VotedFor,
		log:                lg,
		lastApplied:        applied,
		commitIndex:        applied,
		heartbeatInterval:  cfg.HeartbeatInterval,
		electionTimeoutMin: cfg.ElectionTimeoutMin,
		electionTimeoutMax: cfg.ElectionTimeoutMax,
		trans:              trans,
		ownTrans:           ownTrans,
		applyFn:            cfg.Apply,
		waiters:            make(map[uint64]chan applyResult),
		snapshotter:        cfg.Snapshotter,
		snapshotThreshold:  cfg.SnapshotThreshold,
		dataDir:            cfg.DataDir,
		snapInFlight:       make(map[string]bool),
		metaPath:           metaPath,
		applyCh:            make(chan struct{}, 1),
		stopCh:             make(chan struct{}),
		rnd:                rand.New(rand.NewSource(time.Now().UnixNano())),
		logger:             cfg.Logger.With("raft_node", cfg.ID),
	}
	n.electionReset = time.Now()
	n.electionTimeout = n.randTimeout()
	return n, nil
}

// Start は選出タイマーと適用ループを開始する。
func (n *Node) Start() {
	n.wg.Add(2)
	go n.run()
	go n.applyLoop()
}

// Stop はノードを停止する。
func (n *Node) Stop() {
	n.stopOnce.Do(func() { close(n.stopCh) })
	n.wg.Wait()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ownTrans {
		n.trans.Close()
	}
	n.log.Close()
}

func (n *Node) randTimeout() time.Duration {
	span := n.electionTimeoutMax - n.electionTimeoutMin
	if span <= 0 {
		return n.electionTimeoutMin
	}
	return n.electionTimeoutMin + time.Duration(n.rnd.Int63n(int64(span)))
}

func (n *Node) clusterSize() int { return len(n.peers) + 1 }
func (n *Node) majority() int    { return n.clusterSize()/2 + 1 }

// Status はノードの現在状態のスナップショットを返す。
type Status struct {
	ID             string
	State          State
	Term           uint64
	LeaderID       string
	LeaderAddr     string
	CommitIndex    uint64
	AppliedIndex   uint64
	SnapshotIndex  uint64 // ログコンパクション済みの境界（なければ 0）
	ElectionsTotal uint64
	Peers          int
}

// GetStatus は現在の状態を返す。
func (n *Node) GetStatus() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return Status{
		ID:             n.id,
		State:          n.state,
		Term:           n.currentTerm,
		LeaderID:       n.leaderID,
		LeaderAddr:     n.leaderAddrLocked(),
		CommitIndex:    n.commitIndex,
		AppliedIndex:   n.lastApplied,
		SnapshotIndex:  n.log.SnapIndex(),
		ElectionsTotal: n.electionsTotal,
		Peers:          n.clusterSize(),
	}
}

// IsLeader はリーダーかどうかを返す。
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state == Leader
}

func (n *Node) leaderAddrLocked() string {
	if n.leaderID == "" {
		return ""
	}
	if n.leaderID == n.id {
		return n.addr
	}
	return n.peers[n.leaderID]
}

// Propose はコマンドをログに追加し、コミット・適用されるまで待つ。
// リーダーでない場合は NotLeaderError を返す。
func (n *Node) Propose(ctx context.Context, command []byte) (any, error) {
	n.mu.Lock()
	if n.state != Leader {
		err := &NotLeaderError{LeaderID: n.leaderID, LeaderAddr: n.leaderAddrLocked()}
		n.mu.Unlock()
		return nil, err
	}
	index := n.log.LastIndex() + 1
	entry := Entry{Term: n.currentTerm, Index: index, Command: command}
	if err := n.log.Append(entry); err != nil {
		n.mu.Unlock()
		return nil, fmt.Errorf("append to raft log: %w", err)
	}
	ch := make(chan applyResult, 1)
	n.waiters[index] = ch
	n.advanceCommitLocked() // 単一ノード構成なら即コミット
	n.broadcastAppendLocked()
	n.mu.Unlock()

	select {
	case r := <-ch:
		return r.res, r.err
	case <-ctx.Done():
		n.mu.Lock()
		delete(n.waiters, index)
		n.mu.Unlock()
		return nil, ctx.Err()
	case <-n.stopCh:
		return nil, ErrShutdown
	}
}

// ---- メインループ ----

func (n *Node) run() {
	defer n.wg.Done()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
		}
		n.tick()
	}
}

func (n *Node) tick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch n.state {
	case Leader:
		if time.Since(n.lastHeartbeat) >= n.heartbeatInterval {
			n.lastHeartbeat = time.Now()
			n.broadcastAppendLocked()
		}
	default:
		if time.Since(n.electionReset) >= n.electionTimeout {
			n.startElectionLocked()
		}
	}
}

// ---- リーダー選出 ----

func (n *Node) startElectionLocked() {
	n.state = Candidate
	n.currentTerm++
	n.votedFor = n.id
	n.leaderID = ""
	n.electionsTotal++
	n.electionReset = time.Now()
	n.electionTimeout = n.randTimeout()
	if err := saveMeta(n.metaPath, meta{Term: n.currentTerm, VotedFor: n.votedFor}); err != nil {
		n.logger.Error("failed to persist meta", "err", err)
		return
	}
	term := n.currentTerm
	n.logger.Info("starting election", "term", term)

	if n.clusterSize() == 1 {
		n.becomeLeaderLocked()
		return
	}

	req := &raftpb.RequestVoteRequest{
		Term:         term,
		CandidateId:  n.id,
		LastLogIndex: n.log.LastIndex(),
		LastLogTerm:  n.log.LastTerm(),
	}
	votes := 1
	for peerID := range n.peers {
		go func(peerID string) {
			ctx, cancel := context.WithTimeout(context.Background(), n.electionTimeoutMin)
			defer cancel()
			resp, err := n.trans.RequestVote(ctx, peerID, req)
			if err != nil {
				return
			}
			n.mu.Lock()
			defer n.mu.Unlock()
			if resp.Term > n.currentTerm {
				n.becomeFollowerLocked(resp.Term)
				return
			}
			if n.state != Candidate || n.currentTerm != term || !resp.VoteGranted {
				return
			}
			votes++
			if votes >= n.majority() {
				n.becomeLeaderLocked()
			}
		}(peerID)
	}
}

func (n *Node) becomeLeaderLocked() {
	n.state = Leader
	n.leaderID = n.id
	n.nextIndex = make(map[string]uint64, len(n.peers))
	n.matchIndex = make(map[string]uint64, len(n.peers))
	for peerID := range n.peers {
		n.nextIndex[peerID] = n.log.LastIndex() + 1
		n.matchIndex[peerID] = 0
	}
	n.logger.Info("became leader", "term", n.currentTerm)

	// 現 term のエントリをコミットできるようにするための no-op エントリ
	noop := Entry{Term: n.currentTerm, Index: n.log.LastIndex() + 1}
	if err := n.log.Append(noop); err != nil {
		n.logger.Error("failed to append no-op entry", "err", err)
	}
	n.advanceCommitLocked()
	n.lastHeartbeat = time.Now()
	n.broadcastAppendLocked()
}

func (n *Node) becomeFollowerLocked(term uint64) {
	prevState := n.state
	n.state = Follower
	if term > n.currentTerm {
		n.currentTerm = term
		n.votedFor = ""
		if err := saveMeta(n.metaPath, meta{Term: n.currentTerm, VotedFor: n.votedFor}); err != nil {
			n.logger.Error("failed to persist meta", "err", err)
		}
	}
	n.electionReset = time.Now()
	n.electionTimeout = n.randTimeout()
	if prevState == Leader {
		// リーダー退任。待機中の Propose にはエラーを返す
		// （エントリ自体は新リーダーの下でコミットされる可能性がある）。
		for idx, ch := range n.waiters {
			ch <- applyResult{err: &NotLeaderError{LeaderID: n.leaderID, LeaderAddr: n.leaderAddrLocked()}}
			delete(n.waiters, idx)
		}
		n.logger.Info("stepped down", "term", n.currentTerm)
	}
}

// ---- ログ複製 ----

func (n *Node) broadcastAppendLocked() {
	term := n.currentTerm
	for peerID := range n.peers {
		go n.sendAppend(peerID, term)
	}
}

func (n *Node) sendAppend(peerID string, term uint64) {
	n.mu.Lock()
	if n.state != Leader || n.currentTerm != term {
		n.mu.Unlock()
		return
	}
	next := n.nextIndex[peerID]
	// 必要なエントリがコンパクション済みならスナップショットを転送する
	if next <= n.log.SnapIndex() {
		if !n.snapInFlight[peerID] {
			n.snapInFlight[peerID] = true
			go n.sendSnapshot(peerID, term)
		}
		n.mu.Unlock()
		return
	}
	prevIndex := next - 1
	req := &raftpb.AppendEntriesRequest{
		Term:         term,
		LeaderId:     n.id,
		PrevLogIndex: prevIndex,
		PrevLogTerm:  n.log.TermAt(prevIndex),
		LeaderCommit: n.commitIndex,
	}
	for _, e := range n.log.From(next) {
		req.Entries = append(req.Entries, &raftpb.LogEntry{Term: e.Term, Index: e.Index, Command: e.Command})
	}
	n.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), n.heartbeatInterval*4)
	defer cancel()
	resp, err := n.trans.AppendEntries(ctx, peerID, req)
	if err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if resp.Term > n.currentTerm {
		n.becomeFollowerLocked(resp.Term)
		return
	}
	if n.state != Leader || n.currentTerm != term {
		return
	}
	if resp.Success {
		match := prevIndex + uint64(len(req.Entries))
		if match > n.matchIndex[peerID] {
			n.matchIndex[peerID] = match
		}
		n.nextIndex[peerID] = n.matchIndex[peerID] + 1
		n.advanceCommitLocked()
		return
	}
	// 不一致: フォロワーのログ末尾ヒントを使って nextIndex を戻す
	newNext := resp.MatchIndex + 1
	if newNext >= n.nextIndex[peerID] {
		newNext = n.nextIndex[peerID] - 1
	}
	if newNext < 1 {
		newNext = 1
	}
	n.nextIndex[peerID] = newNext
}

// ---- スナップショット ----

// sendSnapshot はディスク上のスナップショットをピアに転送する。
// 成功したら matchIndex / nextIndex をスナップショット境界まで進める。
func (n *Node) sendSnapshot(peerID string, term uint64) {
	defer func() {
		n.mu.Lock()
		delete(n.snapInFlight, peerID)
		n.mu.Unlock()
	}()

	snap, err := LoadSnapshot(n.dataDir)
	if err != nil || snap == nil {
		n.logger.Error("failed to load snapshot for peer", "peer", peerID, "err", err)
		return
	}
	req := &raftpb.InstallSnapshotRequest{
		Term:              term,
		LeaderId:          n.id,
		LastIncludedIndex: snap.Index,
		LastIncludedTerm:  snap.Term,
		Data:              snap.Data,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := n.trans.InstallSnapshot(ctx, peerID, req)
	if err != nil {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if resp.Term > n.currentTerm {
		n.becomeFollowerLocked(resp.Term)
		return
	}
	if n.state != Leader || n.currentTerm != term {
		return
	}
	if snap.Index > n.matchIndex[peerID] {
		n.matchIndex[peerID] = snap.Index
	}
	n.nextIndex[peerID] = n.matchIndex[peerID] + 1
	n.logger.Info("snapshot installed on peer", "peer", peerID, "index", snap.Index)
}

// maybeSnapshotLocked 相当の判定＋実行。applyLoop からエントリ適用後に呼ばれる。
// スナップショット境界から SnapshotThreshold エントリ以上適用が進んでいたら、
// ステートマシンの状態を永続化してログをコンパクションする。
func (n *Node) maybeSnapshot() {
	if n.snapshotter == nil {
		return
	}
	n.mu.Lock()
	idx := n.lastApplied
	if idx == 0 || idx-n.log.SnapIndex() < n.snapshotThreshold {
		n.mu.Unlock()
		return
	}
	term := n.log.TermAt(idx)
	n.mu.Unlock()
	if term == 0 {
		return
	}

	// applyLoop はこのスナップショットが終わるまで次のエントリを適用しないため、
	// ここで取得する状態は lastApplied ちょうどの内容になる。
	data, err := n.snapshotter.Snapshot()
	if err != nil {
		n.logger.Error("failed to serialize state machine", "err", err)
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if idx <= n.log.SnapIndex() { // InstallSnapshot で先にコンパクション済み
		return
	}
	if err := SaveSnapshot(n.dataDir, &Snapshot{Index: idx, Term: term, Data: data}); err != nil {
		n.logger.Error("failed to save snapshot", "err", err)
		return
	}
	if err := n.log.CompactTo(idx, term); err != nil {
		n.logger.Error("failed to compact raft log", "err", err)
		return
	}
	if err := n.snapshotter.Compacted(idx); err != nil {
		n.logger.Error("failed to compact state machine wal", "err", err)
	}
	n.logger.Info("snapshot taken", "index", idx, "term", term)
}

// advanceCommitLocked は過半数に複製されたエントリまで commitIndex を進める。
// Raft の安全性のため、現 term のエントリのみ数える（論文 §5.4.2）。
func (n *Node) advanceCommitLocked() {
	for idx := n.commitIndex + 1; idx <= n.log.LastIndex(); idx++ {
		if n.log.TermAt(idx) != n.currentTerm {
			continue
		}
		count := 1 // 自分
		for _, m := range n.matchIndex {
			if m >= idx {
				count++
			}
		}
		if count >= n.majority() {
			n.commitIndex = idx
		}
	}
	n.signalApply()
}

func (n *Node) signalApply() {
	select {
	case n.applyCh <- struct{}{}:
	default:
	}
}

// ---- RPC ハンドラー ----

func (n *Node) handleRequestVote(req *raftpb.RequestVoteRequest) *raftpb.RequestVoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if req.Term > n.currentTerm {
		n.becomeFollowerLocked(req.Term)
	}
	resp := &raftpb.RequestVoteResponse{Term: n.currentTerm}
	if req.Term < n.currentTerm {
		return resp
	}
	upToDate := req.LastLogTerm > n.log.LastTerm() ||
		(req.LastLogTerm == n.log.LastTerm() && req.LastLogIndex >= n.log.LastIndex())
	if (n.votedFor == "" || n.votedFor == req.CandidateId) && upToDate {
		n.votedFor = req.CandidateId
		if err := saveMeta(n.metaPath, meta{Term: n.currentTerm, VotedFor: n.votedFor}); err != nil {
			n.logger.Error("failed to persist meta", "err", err)
			return resp
		}
		n.electionReset = time.Now()
		n.electionTimeout = n.randTimeout()
		resp.VoteGranted = true
	}
	return resp
}

func (n *Node) handleAppendEntries(req *raftpb.AppendEntriesRequest) *raftpb.AppendEntriesResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := &raftpb.AppendEntriesResponse{Term: n.currentTerm, MatchIndex: n.log.LastIndex()}
	if req.Term < n.currentTerm {
		return resp
	}
	if req.Term > n.currentTerm || n.state != Follower {
		n.becomeFollowerLocked(req.Term)
	}
	resp.Term = n.currentTerm
	n.leaderID = req.LeaderId
	n.electionReset = time.Now()

	// ログ整合性チェック
	if req.PrevLogIndex > 0 {
		if n.log.LastIndex() < req.PrevLogIndex || n.log.TermAt(req.PrevLogIndex) != req.PrevLogTerm {
			hint := n.log.LastIndex()
			if req.PrevLogIndex-1 < hint {
				hint = req.PrevLogIndex - 1
			}
			resp.MatchIndex = hint
			return resp
		}
	}

	// エントリの追記（term が食い違うエントリ以降は切り捨てて上書き）
	for _, pe := range req.Entries {
		e := Entry{Term: pe.Term, Index: pe.Index, Command: pe.Command}
		switch {
		case e.Index <= n.log.SnapIndex():
			// スナップショットでカバー済み（コミット済みなので一致が保証される）
		case e.Index <= n.log.LastIndex() && n.log.TermAt(e.Index) == e.Term:
			// 既に持っている
		case e.Index <= n.log.LastIndex():
			if err := n.log.TruncateFrom(e.Index); err != nil {
				n.logger.Error("failed to truncate raft log", "err", err)
				return resp
			}
			fallthrough
		default:
			if err := n.log.Append(e); err != nil {
				n.logger.Error("failed to append raft log", "err", err)
				return resp
			}
		}
	}

	if req.LeaderCommit > n.commitIndex {
		ci := req.LeaderCommit
		if last := n.log.LastIndex(); ci > last {
			ci = last
		}
		n.commitIndex = ci
		n.signalApply()
	}

	resp.Success = true
	resp.MatchIndex = req.PrevLogIndex + uint64(len(req.Entries))
	return resp
}

func (n *Node) handleInstallSnapshot(req *raftpb.InstallSnapshotRequest) *raftpb.InstallSnapshotResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := &raftpb.InstallSnapshotResponse{Term: n.currentTerm}
	if req.Term < n.currentTerm {
		return resp
	}
	if req.Term > n.currentTerm || n.state != Follower {
		n.becomeFollowerLocked(req.Term)
	}
	resp.Term = n.currentTerm
	n.leaderID = req.LeaderId
	n.electionReset = time.Now()

	// 既にカバー済みの範囲なら何もしない（成功として返し、リーダーは
	// nextIndex を境界+1 に進めて通常の複製に戻る）
	if req.LastIncludedIndex <= n.commitIndex {
		return resp
	}
	if n.snapshotter == nil {
		n.logger.Error("received snapshot but no snapshotter configured")
		return resp
	}

	// 先に自分のスナップショットとして永続化してから状態を置き換える。
	// 途中でクラッシュしても再起動時にスナップショットから復元できる。
	snap := &Snapshot{Index: req.LastIncludedIndex, Term: req.LastIncludedTerm, Data: req.Data}
	if err := SaveSnapshot(n.dataDir, snap); err != nil {
		n.logger.Error("failed to save received snapshot", "err", err)
		return resp
	}
	if err := n.snapshotter.Restore(req.LastIncludedIndex, req.Data); err != nil {
		n.logger.Error("failed to restore state machine from snapshot", "err", err)
		return resp
	}
	if err := n.log.CompactTo(req.LastIncludedIndex, req.LastIncludedTerm); err != nil {
		n.logger.Error("failed to compact raft log after snapshot", "err", err)
	}
	n.commitIndex = req.LastIncludedIndex
	n.lastApplied = req.LastIncludedIndex
	n.logger.Info("installed snapshot from leader",
		"leader", req.LeaderId, "index", req.LastIncludedIndex, "term", req.LastIncludedTerm)
	return resp
}

// ---- 適用ループ ----

func (n *Node) applyLoop() {
	defer n.wg.Done()
	for {
		select {
		case <-n.stopCh:
			return
		case <-n.applyCh:
		}
		for {
			n.mu.Lock()
			if n.lastApplied >= n.commitIndex {
				n.mu.Unlock()
				break
			}
			n.lastApplied++
			idx := n.lastApplied
			entry, ok := n.log.At(idx)
			waiter := n.waiters[idx]
			delete(n.waiters, idx)
			n.mu.Unlock()
			if !ok {
				n.logger.Error("committed entry missing from log", "index", idx)
				continue
			}

			var res any
			var err error
			if len(entry.Command) > 0 {
				res, err = n.applyFn(idx, entry.Command)
			}
			if waiter != nil {
				waiter <- applyResult{res: res, err: err}
			}
			n.maybeSnapshot()
		}
	}
}
