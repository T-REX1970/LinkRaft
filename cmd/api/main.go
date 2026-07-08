// cmd/api は LinkRaft の REST API サーバーのエントリーポイント。
// ストレージとして自作分散 KVS クラスタに gRPC で接続する。
//
// 例:
//
//	api -addr :8080 -kvs localhost:9000,localhost:9001,localhost:9002
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/noda/linkraft/internal/api"
	"github.com/noda/linkraft/internal/api/handler"
	"github.com/noda/linkraft/internal/kvs"
)

func main() {
	var (
		addr     = flag.String("addr", envOr("API_ADDR", ":8080"), "HTTP リッスンアドレス")
		kvsAddrs = flag.String("kvs", envOr("KVS_ADDRS", "localhost:9000,localhost:9001,localhost:9002"), "KVS ノードアドレス（カンマ区切り）")
		secret   = flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "JWT 署名シークレット")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *secret == "" {
		*secret = "linkraft-dev-secret"
		logger.Warn("JWT_SECRET is not set; using an insecure development secret")
	}

	addrs := []string{}
	for _, a := range strings.Split(*kvsAddrs, ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		logger.Error("no KVS node addresses given")
		os.Exit(1)
	}

	client := kvs.NewClient(addrs)
	defer client.Close()

	h := handler.New(client, []byte(*secret))
	e := api.NewRouter(h, []byte(*secret))

	go func() {
		if err := e.Start(*addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()
	logger.Info("api server started", "addr", *addr, "kvs", addrs)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
