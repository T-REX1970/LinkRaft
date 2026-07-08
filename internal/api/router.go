// Package api は Echo のルーティングを組み立てる。
package api

import (
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"github.com/noda/linkraft/internal/api/handler"
	"github.com/noda/linkraft/internal/api/middleware"
)

// NewRouter はハンドラーを配線した Echo インスタンスを返す。
func NewRouter(h *handler.Handler, jwtSecret []byte) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(echomw.Recover())
	e.Use(echomw.Logger())
	e.Use(echomw.CORS()) // 開発時の Vite dev server (別ポート) からのアクセス用

	pub := e.Group("/api")
	pub.POST("/auth/signup", h.Signup)
	pub.POST("/auth/login", h.Login)
	pub.GET("/links", h.ListLinks)
	pub.GET("/links/:id", h.GetLink)
	pub.GET("/links/:id/comments", h.ListComments)
	pub.GET("/users/:id", h.GetUser)
	pub.POST("/ogp", h.FetchOGP)
	pub.GET("/health", h.Health)

	auth := e.Group("/api", middleware.JWT(jwtSecret))
	auth.POST("/links", h.CreateLink)
	auth.DELETE("/links/:id", h.DeleteLink)
	auth.POST("/links/:id/vote", h.ToggleVote)
	auth.POST("/links/:id/comments", h.CreateComment)
	auth.DELETE("/comments/:id", h.DeleteComment)

	return e
}
