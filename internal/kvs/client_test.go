package kvs

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/noda/linkraft/internal/raft"
	"github.com/noda/linkraft/proto/kvspb"
	"github.com/noda/linkraft/proto/raftpb"
)

// startTestCluster は 3 ノードの KVS クラスタを in-process で起動する。
func startTestCluster(t *testing.T) *Client {
	t.Helper()
	const size = 3

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

	for i := 0; i < size; i++ {
		id := fmt.Sprintf("node-%d", i)
		peers := make(map[string]string)
		for j := range addrs {
			if j != i {
				peers[fmt.Sprintf("node-%d", j)] = addrs[j]
			}
		}
		store, err := OpenStore(filepath.Join(t.TempDir(), "kvs.wal"))
		if err != nil {
			t.Fatalf("OpenStore: %v", err)
		}
		node, err := raft.NewNode(raft.Config{
			ID:                 id,
			Addr:               addrs[i],
			Peers:              peers,
			DataDir:            t.TempDir(),
			ElectionTimeoutMin: 150 * time.Millisecond,
			ElectionTimeoutMax: 300 * time.Millisecond,
			HeartbeatInterval:  30 * time.Millisecond,
			Apply: func(index uint64, command []byte) (any, error) {
				cmd, err := DecodeCommand(command)
				if err != nil {
					return nil, err
				}
				return store.Apply(index, cmd)
			},
		})
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		srv := grpc.NewServer()
		raftpb.RegisterRaftServer(srv, raft.NewServer(node))
		kvspb.RegisterKVSServer(srv, NewServer(store, node))
		go srv.Serve(listeners[i])
		node.Start()
		t.Cleanup(func() {
			node.Stop()
			srv.Stop()
			store.Close()
		})
	}

	client := NewClient(addrs)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestClientEndToEnd(t *testing.T) {
	client := startTestCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Set / Get
	if err := client.Set(ctx, "link:1", []byte(`{"title":"hello"}`)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, found, err := client.Get(ctx, "link:1")
	if err != nil || !found || string(v) != `{"title":"hello"}` {
		t.Fatalf("Get = %q, %v, %v", v, found, err)
	}

	// Incr
	for want := int64(1); want <= 3; want++ {
		n, err := client.Incr(ctx, "seq:link")
		if err != nil || n != want {
			t.Fatalf("Incr = %d, %v; want %d", n, err, want)
		}
	}

	// Keys
	if err := client.Set(ctx, "link:2", []byte("x")); err != nil {
		t.Fatalf("Set link:2: %v", err)
	}
	keys, err := client.Keys(ctx, "link:")
	if err != nil || len(keys) != 2 {
		t.Fatalf("Keys = %v, %v; want 2 keys", keys, err)
	}

	// Delete
	if err := client.Delete(ctx, "link:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := client.Get(ctx, "link:1"); err != nil || found {
		t.Fatalf("Get after Delete: found=%v err=%v", found, err)
	}
}

func TestClientClusterStatus(t *testing.T) {
	client := startTestCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 書き込みが成功する時点でリーダーは確定している
	if err := client.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	statuses := client.ClusterStatus(ctx)
	if len(statuses) != 3 {
		t.Fatalf("got %d statuses, want 3", len(statuses))
	}
	leaders := 0
	for _, st := range statuses {
		switch st.State {
		case "leader":
			leaders++
		case "follower", "candidate":
		default:
			t.Fatalf("unexpected state %q for %s", st.State, st.Address)
		}
	}
	if leaders != 1 {
		t.Fatalf("got %d leaders, want 1", leaders)
	}
}
