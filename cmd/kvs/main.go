// cmd/kvs は分散 KVS ノードのエントリーポイント。
// Raft で合意した書き込みをインメモリストア + WAL に適用し、
// kvspb.KVS / raftpb.Raft の 2 つの gRPC サービスを 1 つのポートで提供する。
//
// 例（3 ノードクラスタの node-0）:
//
//	kvs -id node-0 -listen :9000 -advertise localhost:9000 \
//	    -peers node-1=localhost:9001,node-2=localhost:9002 -data ./data/node-0
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"google.golang.org/grpc"

	"github.com/noda/linkraft/internal/kvs"
	"github.com/noda/linkraft/internal/raft"
	"github.com/noda/linkraft/proto/kvspb"
	"github.com/noda/linkraft/proto/raftpb"
)

func main() {
	var (
		id        = flag.String("id", envOr("KVS_NODE_ID", "node-0"), "ノード ID")
		listen    = flag.String("listen", envOr("KVS_LISTEN", ":9000"), "gRPC リッスンアドレス")
		advertise = flag.String("advertise", envOr("KVS_ADVERTISE", "localhost:9000"), "他ノード・クライアントへ広報するアドレス")
		peersFlag = flag.String("peers", envOr("KVS_PEERS", ""), "他ノード一覧 (id=addr,id=addr)")
		dataDir   = flag.String("data", envOr("KVS_DATA_DIR", "./data"), "WAL / Raft ログの保存先")
		snapEvery = flag.Uint64("snapshot-threshold", envUint("KVS_SNAPSHOT_THRESHOLD", 1000), "この数のエントリを適用するごとにスナップショットを取る")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*id, *listen, *advertise, *peersFlag, *dataDir, *snapEvery, logger); err != nil {
		logger.Error("kvs node exited with error", "err", err)
		os.Exit(1)
	}
}

func run(id, listen, advertise, peersFlag, dataDir string, snapEvery uint64, logger *slog.Logger) error {
	peers, err := parsePeers(peersFlag)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// スナップショットがあればそこから復元し、以降の WAL レコードだけを再生する
	snap, err := raft.LoadSnapshot(dataDir)
	if err != nil {
		return fmt.Errorf("load snapshot: %w", err)
	}
	var snapIndex uint64
	var snapData []byte
	if snap != nil {
		snapIndex, snapData = snap.Index, snap.Data
	}
	store, err := kvs.OpenStoreAt(filepath.Join(dataDir, "kvs.wal"), snapIndex, snapData)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	node, err := raft.NewNode(raft.Config{
		ID:                id,
		Addr:              advertise,
		Peers:             peers,
		DataDir:           dataDir,
		AppliedIndex:      store.AppliedIndex(),
		Snapshotter:       store,
		SnapshotThreshold: snapEvery,
		Logger:            logger,
		Apply: func(index uint64, command []byte) (any, error) {
			cmd, err := kvs.DecodeCommand(command)
			if err != nil {
				return nil, err
			}
			return store.Apply(index, cmd)
		},
	})
	if err != nil {
		return fmt.Errorf("create raft node: %w", err)
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}

	srv := grpc.NewServer()
	raftpb.RegisterRaftServer(srv, raft.NewServer(node))
	kvspb.RegisterKVSServer(srv, kvs.NewServer(store, node))

	node.Start()
	logger.Info("kvs node started",
		"id", id, "listen", listen, "advertise", advertise,
		"peers", peersFlag, "data_dir", dataDir,
		"restored_keys", store.Len(), "applied_index", store.AppliedIndex())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	srv.GracefulStop()
	node.Stop()
	return nil
}

// parsePeers は "node-1=host:9001,node-2=host:9002" 形式をパースする。
func parsePeers(s string) (map[string]string, error) {
	peers := make(map[string]string)
	if strings.TrimSpace(s) == "" {
		return peers, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, addr, ok := strings.Cut(part, "=")
		if !ok || id == "" || addr == "" {
			return nil, errors.New("invalid -peers format (want id=addr,id=addr): " + part)
		}
		peers[id] = addr
	}
	return peers, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envUint(key string, def uint64) uint64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
