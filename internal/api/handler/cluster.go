package handler

import (
	"net/http"
	"sort"

	"github.com/labstack/echo/v4"
)

// AddClusterMember は POST /api/cluster/members。
// -join モードで起動済みのノードをクラスタに追加する（リーダーに転送される）。
func (h *Handler) AddClusterMember(c echo.Context) error {
	var req struct {
		ID   string `json:"id"`
		Addr string `json:"addr"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.ID == "" || req.Addr == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id and addr are required")
	}
	if err := h.repo.kv.AddMember(c.Request().Context(), req.ID, req.Addr); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

// RemoveClusterMember は DELETE /api/cluster/members/:id。
func (h *Handler) RemoveClusterMember(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is required")
	}
	if err := h.repo.kv.RemoveMember(c.Request().Context(), id); err != nil {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

type tagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListTags は GET /api/tags。使用中のタグと件数を件数順で返す（投稿フォームのサジェスト用）。
func (h *Handler) ListTags(c echo.Context) error {
	ctx := c.Request().Context()
	keys, err := h.repo.kv.Keys(ctx, indexTag(""))
	if err != nil {
		return err
	}
	tags := make([]tagCount, 0, len(keys))
	prefixLen := len(indexTag(""))
	for _, key := range keys {
		ids, err := h.repo.Index(ctx, key)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue // 全リンク削除済みのタグ
		}
		tags = append(tags, tagCount{Name: key[prefixLen:], Count: len(ids)})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Name < tags[j].Name
	})
	return c.JSON(http.StatusOK, echo.Map{"tags": tags})
}
