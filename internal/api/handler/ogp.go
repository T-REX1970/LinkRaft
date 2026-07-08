package handler

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"
)

type ogpRequest struct {
	URL string `json:"url"`
}

type ogpResponse struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

// FetchOGP は POST /api/ogp。指定 URL の OGP 情報を取得する（投稿フォームのプリフィル用）。
func (h *Handler) FetchOGP(c echo.Context) error {
	var req ogpRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url must be a valid http(s) URL")
	}

	httpReq, err := http.NewRequestWithContext(c.Request().Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("User-Agent", "LinkRaft-OGP/1.0")
	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "failed to fetch url")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return echo.NewHTTPError(http.StatusBadGateway, "target returned non-2xx status")
	}

	ogp := parseOGP(io.LimitReader(resp.Body, 1<<20))
	return c.JSON(http.StatusOK, ogp)
}

// parseOGP は HTML から og:title / og:description / og:image と <title> を抽出する。
func parseOGP(r io.Reader) ogpResponse {
	var out ogpResponse
	var pageTitle string

	z := html.NewTokenizer(r)
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			if out.Title == "" {
				out.Title = strings.TrimSpace(pageTitle)
			}
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "meta":
				var prop, content string
				for _, a := range t.Attr {
					switch a.Key {
					case "property", "name":
						prop = a.Val
					case "content":
						content = a.Val
					}
				}
				switch prop {
				case "og:title":
					out.Title = content
				case "og:description", "description":
					if out.Description == "" || prop == "og:description" {
						out.Description = content
					}
				case "og:image":
					out.Image = content
				}
			case "title":
				inTitle = true
			case "body":
				// head を抜けたら十分
				if out.Title == "" {
					out.Title = strings.TrimSpace(pageTitle)
				}
				return out
			}
		case html.TextToken:
			if inTitle {
				pageTitle += z.Token().Data
			}
		case html.EndTagToken:
			if z.Token().Data == "title" {
				inTitle = false
			}
		}
	}
}
