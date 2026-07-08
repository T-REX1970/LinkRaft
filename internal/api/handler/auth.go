package handler

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"

	"github.com/noda/linkraft/internal/api/middleware"
	"github.com/noda/linkraft/internal/model"
)

type signupRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	User  model.PublicUser `json:"user"`
	Token string           `json:"token"`
}

// Signup は POST /api/auth/signup。
func (h *Handler) Signup(c echo.Context) error {
	var req signupRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = normalizeEmail(req.Email)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 50 {
		return echo.NewHTTPError(http.StatusBadRequest, "name must be 1-50 characters")
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid email address")
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		return echo.NewHTTPError(http.StatusBadRequest, "password must be 8-72 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	h.repo.Lock()
	defer h.repo.Unlock()

	if _, found, err := h.repo.kv.Get(ctx, emailKey(req.Email)); err != nil {
		return err
	} else if found {
		return echo.NewHTTPError(http.StatusConflict, "email already registered")
	}

	id, err := h.repo.kv.Incr(ctx, seqUser)
	if err != nil {
		return err
	}
	user := model.User{
		ID:           id,
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: string(hash),
		CreatedAt:    time.Now().UTC(),
	}
	if err := h.repo.SetJSON(ctx, userKey(id), user); err != nil {
		return err
	}
	if err := h.repo.kv.Set(ctx, emailKey(req.Email), []byte(strconv.FormatInt(id, 10))); err != nil {
		return err
	}

	token, err := middleware.NewToken(h.jwtSecret, user)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, authResponse{User: user.Public(), Token: token})
}

// Login は POST /api/auth/login。
func (h *Handler) Login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Email = normalizeEmail(req.Email)

	ctx := c.Request().Context()
	idBytes, found, err := h.repo.kv.Get(ctx, emailKey(req.Email))
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
	}
	id, err := strconv.ParseInt(string(idBytes), 10, 64)
	if err != nil {
		return err
	}

	var user model.User
	if found, err := h.repo.GetJSON(ctx, userKey(id), &user); err != nil {
		return err
	} else if !found {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
	}

	token, err := middleware.NewToken(h.jwtSecret, user)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, authResponse{User: user.Public(), Token: token})
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
