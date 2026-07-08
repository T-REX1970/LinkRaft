// Package handler は Echo の HTTP ハンドラー群。
// ストレージは自作分散 KVS（Raft）で、KV インターフェース経由でアクセスする。
package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// Handler は全エンドポイントのハンドラーを束ねる。
type Handler struct {
	repo       *Repo
	jwtSecret  []byte
	httpClient *http.Client // OGP 取得用
}

// New は Handler を作る。
func New(kv KV, jwtSecret []byte) *Handler {
	return &Handler{
		repo:       NewRepo(kv),
		jwtSecret:  jwtSecret,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// pathID は :id パスパラメータを int64 として取り出す。
func pathID(c echo.Context) (int64, error) {
	var id int64
	if err := echo.PathParamsBinder(c).Int64("id", &id).BindError(); err != nil || id <= 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	return id, nil
}
