// Package api は Echo のルーティングを組み立てる。
package api

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"github.com/noda/linkraft/internal/api/handler"
	"github.com/noda/linkraft/internal/api/middleware"
)

// NewRouter はハンドラーを配線した Echo インスタンスを返す。
// webDir にフロントエンドのビルド成果物 (web/dist) があれば静的配信する。
func NewRouter(h *handler.Handler, jwtSecret []byte, webDir string) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(echomw.Recover())
	e.Use(echomw.Logger())
	e.Use(echomw.CORS()) // 開発時の Vite dev server (別ポート) からのアクセス用

	// SPA なので存在しないパスは index.html にフォールバックする（HTML5: true）。
	if webDir != "" {
		if _, err := os.Stat(filepath.Join(webDir, "index.html")); err == nil {
			e.Use(echomw.StaticWithConfig(echomw.StaticConfig{
				Root:  webDir,
				HTML5: true,
				Skipper: func(c echo.Context) bool {
					return strings.HasPrefix(c.Request().URL.Path, "/api")
				},
			}))
		}
	}

	// 公開エンドポイントでもトークンがあれば閲覧者を識別する（voted フラグ用）
	pub := e.Group("/api", middleware.JWTOptional(jwtSecret))
	pub.POST("/auth/signup", h.Signup)
	pub.POST("/auth/login", h.Login)
	pub.GET("/links", h.ListLinks)
	pub.GET("/links/:id", h.GetLink)
	pub.GET("/links/:id/comments", h.ListComments)
	pub.GET("/users/:id", h.GetUser)
	pub.POST("/ogp", h.FetchOGP)
	pub.GET("/health", h.Health)
	pub.GET("/tags", h.ListTags)

	auth := e.Group("/api", middleware.JWT(jwtSecret))
	auth.POST("/links", h.CreateLink)
	auth.DELETE("/links/:id", h.DeleteLink)
	auth.POST("/links/:id/vote", h.ToggleVote)
	auth.POST("/links/:id/comments", h.CreateComment)
	auth.DELETE("/comments/:id", h.DeleteComment)
	// クラスタ管理（メンバーシップ変更）。学習用アプリなのでログインユーザーなら
	// 誰でも操作できる（実運用なら管理者権限で保護する）
	auth.POST("/cluster/members", h.AddClusterMember)
	auth.DELETE("/cluster/members/:id", h.RemoveClusterMember)

	return e
}
