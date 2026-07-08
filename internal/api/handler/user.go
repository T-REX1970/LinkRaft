package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/model"
)

// GetUser は GET /api/users/:id。プロフィールと投稿リンク、獲得総投票数を返す。
func (h *Handler) GetUser(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	var user model.User
	found, err := h.repo.GetJSON(ctx, userKey(id), &user)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}

	ids, err := h.repo.Index(ctx, indexUserLinks(id))
	if err != nil {
		return err
	}
	links, err := h.fetchLinks(ctx, ids)
	if err != nil {
		return err
	}
	totalVotes := 0
	for _, l := range links {
		totalVotes += l.VoteCount
	}
	return c.JSON(http.StatusOK, echo.Map{
		"user":        user.Public(),
		"links":       links,
		"total_votes": totalVotes,
	})
}
