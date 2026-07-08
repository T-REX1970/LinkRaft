// Package middleware は Echo 用の認証ミドルウェアと JWT ヘルパーを提供する。
package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/model"
)

const (
	ctxUserID   = "auth_user_id"
	ctxUserName = "auth_user_name"
)

// Claims は LinkRaft の JWT クレーム。
type Claims struct {
	UserID   int64  `json:"uid"`
	UserName string `json:"name"`
	jwt.RegisteredClaims
}

// NewToken はユーザーの JWT（有効期限 7 日）を発行する。
func NewToken(secret []byte, u model.User) (string, error) {
	claims := Claims{
		UserID:   u.ID,
		UserName: u.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", u.ID),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// JWT は Authorization: Bearer トークンを検証するミドルウェア。
// 検証に成功するとコンテキストにユーザー ID と名前をセットする。
func JWT(secret []byte) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			auth := c.Request().Header.Get(echo.HeaderAuthorization)
			token, ok := strings.CutPrefix(auth, "Bearer ")
			if !ok || token == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing bearer token")
			}
			claims := &Claims{}
			parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return secret, nil
			})
			if err != nil || !parsed.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
			c.Set(ctxUserID, claims.UserID)
			c.Set(ctxUserName, claims.UserName)
			return next(c)
		}
	}
}

// UserID は JWT ミドルウェアがセットした認証済みユーザー ID を返す。
func UserID(c echo.Context) int64 {
	if v, ok := c.Get(ctxUserID).(int64); ok {
		return v
	}
	return 0
}

// UserName は認証済みユーザーの表示名を返す。
func UserName(c echo.Context) string {
	if v, ok := c.Get(ctxUserName).(string); ok {
		return v
	}
	return ""
}
