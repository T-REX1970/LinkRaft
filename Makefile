# LinkRaft 開発用ショートカット

GO      ?= go
PROTOC  ?= protoc
BIN_DIR := bin

.PHONY: all build web web-dev test lint vet proto clean run-kvs-0 run-kvs-1 run-kvs-2 run-kvs-3-join run-api

all: build

## build: api / kvs バイナリを bin/ にビルド
build:
	$(GO) build -o $(BIN_DIR)/api ./cmd/api
	$(GO) build -o $(BIN_DIR)/kvs ./cmd/kvs

## web: フロントエンドを web/dist にビルド（API サーバーが配信する）
web:
	cd web && npm install && npm run build

## web-dev: Vite dev server を起動（:5173、/api は :8080 にプロキシ）
web-dev:
	cd web && npm install && npm run dev

## test: 全パッケージのテスト（race detector 付き）
test:
	$(GO) test -race ./...

## vet: go vet
vet:
	$(GO) vet ./...

## lint: golangci-lint（インストール済みの場合）
lint:
	golangci-lint run ./...

## proto: Protocol Buffers から Go コードを生成
proto:
	$(PROTOC) --proto_path=proto \
		--go_out=. --go_opt=module=github.com/noda/linkraft \
		--go-grpc_out=. --go-grpc_opt=module=github.com/noda/linkraft \
		proto/raft.proto proto/kvs.proto

## run-kvs-{0,1,2}: ローカルで 3 ノードクラスタを起動（別ターミナルでそれぞれ実行）
run-kvs-0: build
	$(BIN_DIR)/kvs -id node-0 -listen :9000 -advertise localhost:9000 \
		-peers node-1=localhost:9001,node-2=localhost:9002 -data ./data/node-0

run-kvs-1: build
	$(BIN_DIR)/kvs -id node-1 -listen :9001 -advertise localhost:9001 \
		-peers node-0=localhost:9000,node-2=localhost:9002 -data ./data/node-1

run-kvs-2: build
	$(BIN_DIR)/kvs -id node-2 -listen :9002 -advertise localhost:9002 \
		-peers node-0=localhost:9000,node-1=localhost:9001 -data ./data/node-2

## run-kvs-3-join: 4 台目を join モードで起動（Web UI かクラスタ API で AddMember すると参加する）
run-kvs-3-join: build
	$(BIN_DIR)/kvs -id node-3 -listen :9003 -advertise localhost:9003 \
		-peers node-0=localhost:9000,node-1=localhost:9001,node-2=localhost:9002 \
		-data ./data/node-3 -join

## run-api: API サーバーを起動（KVS クラスタ起動後に実行）
run-api: build
	$(BIN_DIR)/api -addr :8080 -kvs localhost:9000,localhost:9001,localhost:9002

clean:
	rm -rf $(BIN_DIR) data
