package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/api/middleware"
	"github.com/noda/linkraft/internal/model"
)

// ToggleVote は POST /api/links/:id/vote（要認証）。
// 未投票なら投票、投票済みなら取り消すトグル式。
func (h *Handler) ToggleVote(c echo.Context) error {
	linkID, err := pathID(c)
	if err != nil {
		return err
	}
	userID := middleware.UserID(c)
	ctx := c.Request().Context()

	h.repo.Lock()
	defer h.repo.Unlock()

	var link model.Link
	found, err := h.repo.GetJSON(ctx, linkKey(linkID), &link)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "link not found")
	}

	vk := voteKey(linkID, userID)
	_, voted, err := h.repo.kv.Get(ctx, vk)
	if err != nil {
		return err
	}

	if voted {
		if err := h.repo.kv.Delete(ctx, vk); err != nil {
			return err
		}
		if link.VoteCount > 0 {
			link.VoteCount--
		}
	} else {
		if err := h.repo.SetJSON(ctx, vk, model.Vote{CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		link.VoteCount++
	}
	if err := h.repo.SetJSON(ctx, linkKey(linkID), link); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, echo.Map{"voted": !voted, "vote_count": link.VoteCount})
}
