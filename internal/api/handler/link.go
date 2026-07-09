package handler

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/api/middleware"
	"github.com/noda/linkraft/internal/model"
)

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,29}$`)

type createLinkRequest struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	ImageURL    string   `json:"image_url"`
	Tags        []string `json:"tags"`
}

// linkView はレスポンス用のリンク表現。閲覧者が投票済みかどうかを付与する。
type linkView struct {
	model.Link
	Voted bool `json:"voted"`
}

type linkListResponse struct {
	Links   []linkView `json:"links"`
	Total   int        `json:"total"`
	Page    int        `json:"page"`
	PerPage int        `json:"per_page"`
}

// ListLinks は GET /api/links。?sort=recent|popular &tag= &q= &page= &per_page=
func (h *Handler) ListLinks(c echo.Context) error {
	sortKey := c.QueryParam("sort")
	if sortKey == "" {
		sortKey = "recent"
	}
	if sortKey != "recent" && sortKey != "popular" {
		return echo.NewHTTPError(http.StatusBadRequest, "sort must be recent or popular")
	}
	page, perPage := 1, 20
	if err := echo.QueryParamsBinder(c).Int("page", &page).Int("per_page", &perPage).BindError(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid pagination params")
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	ctx := c.Request().Context()
	indexKey := indexRecent
	if tag := c.QueryParam("tag"); tag != "" {
		indexKey = indexTag(tag)
	}
	ids, err := h.repo.Index(ctx, indexKey)
	if err != nil {
		return err
	}
	links, err := h.fetchLinks(ctx, ids)
	if err != nil {
		return err
	}

	if q := strings.ToLower(strings.TrimSpace(c.QueryParam("q"))); q != "" {
		filtered := links[:0]
		for _, l := range links {
			hay := strings.ToLower(l.Title + " " + l.Description + " " + l.URL)
			if strings.Contains(hay, q) {
				filtered = append(filtered, l)
			}
		}
		links = filtered
	}

	// 新着順はインデックス順のまま。人気順は投票数で並べ替える
	// （index:links:popular を都度メンテナンスする代わりにクエリ時に計算する）。
	if sortKey == "popular" {
		sort.SliceStable(links, func(i, j int) bool {
			if links[i].VoteCount != links[j].VoteCount {
				return links[i].VoteCount > links[j].VoteCount
			}
			return links[i].CreatedAt.After(links[j].CreatedAt)
		})
	}

	total := len(links)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	views, err := h.withVoted(ctx, links[start:end], middleware.UserID(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, linkListResponse{
		Links: views, Total: total, Page: page, PerPage: perPage,
	})
}

func (h *Handler) fetchLinks(ctx context.Context, ids []int64) ([]model.Link, error) {
	links := make([]model.Link, 0, len(ids))
	for _, id := range ids {
		var l model.Link
		found, err := h.repo.GetJSON(ctx, linkKey(id), &l)
		if err != nil {
			return nil, err
		}
		if found {
			links = append(links, l)
		}
	}
	return links, nil
}

// withVoted はリンク一覧に「閲覧者が投票済みか」を付与する。未ログインなら常に false。
func (h *Handler) withVoted(ctx context.Context, links []model.Link, userID int64) ([]linkView, error) {
	views := make([]linkView, 0, len(links))
	for _, l := range links {
		v := linkView{Link: l}
		if userID != 0 {
			_, voted, err := h.repo.kv.Get(ctx, voteKey(l.ID, userID))
			if err != nil {
				return nil, err
			}
			v.Voted = voted
		}
		views = append(views, v)
	}
	return views, nil
}

// CreateLink は POST /api/links（要認証）。
func (h *Handler) CreateLink(c echo.Context) error {
	var req createLinkRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Description = strings.TrimSpace(req.Description)

	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url must be a valid http(s) URL")
	}
	req.ImageURL = strings.TrimSpace(req.ImageURL)
	if req.ImageURL != "" {
		iu, err := url.Parse(req.ImageURL)
		if err != nil || (iu.Scheme != "http" && iu.Scheme != "https") || iu.Host == "" || len(req.ImageURL) > 2000 {
			return echo.NewHTTPError(http.StatusBadRequest, "image_url must be a valid http(s) URL")
		}
	}
	if req.Title == "" || utf8.RuneCountInString(req.Title) > 200 {
		return echo.NewHTTPError(http.StatusBadRequest, "title must be 1-200 characters")
	}
	if utf8.RuneCountInString(req.Description) > 1000 {
		return echo.NewHTTPError(http.StatusBadRequest, "description must be at most 1000 characters")
	}
	if len(req.Tags) > 5 {
		return echo.NewHTTPError(http.StatusBadRequest, "at most 5 tags")
	}
	tags := make([]string, 0, len(req.Tags))
	seen := map[string]bool{}
	for _, t := range req.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if !tagPattern.MatchString(t) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid tag: "+t)
		}
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}

	ctx := c.Request().Context()
	id, err := h.repo.kv.Incr(ctx, seqLink)
	if err != nil {
		return err
	}
	link := model.Link{
		ID:          id,
		URL:         u.String(),
		Title:       req.Title,
		Description: req.Description,
		ImageURL:    req.ImageURL,
		UserID:      middleware.UserID(c),
		UserName:    middleware.UserName(c),
		Tags:        tags,
		CreatedAt:   time.Now().UTC(),
	}

	h.repo.Lock()
	defer h.repo.Unlock()
	if err := h.repo.SetJSON(ctx, linkKey(id), link); err != nil {
		return err
	}
	if err := h.repo.PrependIndex(ctx, indexRecent, id); err != nil {
		return err
	}
	if err := h.repo.PrependIndex(ctx, indexUserLinks(link.UserID), id); err != nil {
		return err
	}
	for _, t := range tags {
		if err := h.repo.PrependIndex(ctx, indexTag(t), id); err != nil {
			return err
		}
	}
	return c.JSON(http.StatusCreated, echo.Map{"link": linkView{Link: link}})
}

// GetLink は GET /api/links/:id。
func (h *Handler) GetLink(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	var link model.Link
	found, err := h.repo.GetJSON(ctx, linkKey(id), &link)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "link not found")
	}
	views, err := h.withVoted(ctx, []model.Link{link}, middleware.UserID(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, echo.Map{"link": views[0]})
}

// DeleteLink は DELETE /api/links/:id（本人のみ、要認証）。
// リンク本体に加えて、票・コメント・各インデックスからも取り除く。
func (h *Handler) DeleteLink(c echo.Context) error {
	id, err := pathID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	h.repo.Lock()
	defer h.repo.Unlock()

	var link model.Link
	found, err := h.repo.GetJSON(ctx, linkKey(id), &link)
	if err != nil {
		return err
	}
	if !found {
		return echo.NewHTTPError(http.StatusNotFound, "link not found")
	}
	if link.UserID != middleware.UserID(c) {
		return echo.NewHTTPError(http.StatusForbidden, "only the owner can delete this link")
	}

	if err := h.repo.RemoveIndex(ctx, indexRecent, id); err != nil {
		return err
	}
	if err := h.repo.RemoveIndex(ctx, indexUserLinks(link.UserID), id); err != nil {
		return err
	}
	for _, t := range link.Tags {
		if err := h.repo.RemoveIndex(ctx, indexTag(t), id); err != nil {
			return err
		}
	}

	// コメントを削除
	commentIDs, err := h.repo.Index(ctx, indexLinkComments(id))
	if err != nil {
		return err
	}
	for _, cid := range commentIDs {
		if err := h.repo.kv.Delete(ctx, commentKey(cid)); err != nil {
			return err
		}
	}
	if err := h.repo.kv.Delete(ctx, indexLinkComments(id)); err != nil {
		return err
	}

	// 票を削除
	voteKeys, err := h.repo.kv.Keys(ctx, votePrefix(id))
	if err != nil {
		return err
	}
	for _, vk := range voteKeys {
		if err := h.repo.kv.Delete(ctx, vk); err != nil {
			return err
		}
	}

	if err := h.repo.kv.Delete(ctx, linkKey(id)); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
