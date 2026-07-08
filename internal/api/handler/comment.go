package handler

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/api/middleware"
	"github.com/noda/linkraft/internal/model"
)

type createCommentRequest struct {
	Body     string `json:"body"`
	ParentID *int64 `json:"parent_id"`
}

// ListComments は GET /api/links/:id/comments。
func (h *Handler) ListComments(c echo.Context) error {
	linkID, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	var link model.Link
	found, err := h.repo.GetJSON(ctx, linkKey(linkID), &link)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "link not found")
	}

	ids, err := h.repo.Index(ctx, indexLinkComments(linkID))
	if err != nil {
		return err
	}
	comments := make([]model.Comment, 0, len(ids))
	for _, id := range ids {
		var cm model.Comment
		found, err := h.repo.GetJSON(ctx, commentKey(id), &cm)
		if err != nil {
			return err
		}
		if found {
			comments = append(comments, cm)
		}
	}
	return c.JSON(http.StatusOK, echo.Map{"comments": comments})
}

// CreateComment は POST /api/links/:id/comments（要認証）。
// parent_id を指定すると 1 階層の返信になる（返信への返信は不可）。
func (h *Handler) CreateComment(c echo.Context) error {
	linkID, err := pathID(c)
	if err != nil {
		return err
	}
	var req createCommentRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || utf8.RuneCountInString(req.Body) > 2000 {
		return echo.NewHTTPError(http.StatusBadRequest, "body must be 1-2000 characters")
	}

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

	if req.ParentID != nil {
		var parent model.Comment
		found, err := h.repo.GetJSON(ctx, commentKey(*req.ParentID), &parent)
		if err != nil {
			return err
		}
		if !found || parent.LinkID != linkID {
			return echo.NewHTTPError(http.StatusBadRequest, "parent comment not found")
		}
		if parent.ParentID != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "replies are limited to one level")
		}
	}

	id, err := h.repo.kv.Incr(ctx, seqComment)
	if err != nil {
		return err
	}
	comment := model.Comment{
		ID:        id,
		LinkID:    linkID,
		UserID:    middleware.UserID(c),
		UserName:  middleware.UserName(c),
		Body:      req.Body,
		ParentID:  req.ParentID,
		CreatedAt: time.Now().UTC(),
	}
	if err := h.repo.SetJSON(ctx, commentKey(id), comment); err != nil {
		return err
	}
	if err := h.repo.AppendIndex(ctx, indexLinkComments(linkID), id); err != nil {
		return err
	}
	link.CommentCount++
	if err := h.repo.SetJSON(ctx, linkKey(linkID), link); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, echo.Map{"comment": comment})
}

// DeleteComment は DELETE /api/comments/:id（本人のみ、要認証）。
func (h *Handler) DeleteComment(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	h.repo.Lock()
	defer h.repo.Unlock()

	var comment model.Comment
	found, err := h.repo.GetJSON(ctx, commentKey(id), &comment)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	if comment.UserID != middleware.UserID(c) {
		return echo.NewHTTPError(http.StatusForbidden, "only the owner can delete this comment")
	}

	if err := h.repo.RemoveIndex(ctx, indexLinkComments(comment.LinkID), id); err != nil {
		return err
	}
	if err := h.repo.kv.Delete(ctx, commentKey(id)); err != nil {
		return err
	}

	var link model.Link
	if found, err := h.repo.GetJSON(ctx, linkKey(comment.LinkID), &link); err != nil {
		return err
	} else if found && link.CommentCount > 0 {
		link.CommentCount--
		if err := h.repo.SetJSON(ctx, linkKey(comment.LinkID), link); err != nil {
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}
