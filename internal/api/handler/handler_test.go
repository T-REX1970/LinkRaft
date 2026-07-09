package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/noda/linkraft/internal/api"
	"github.com/noda/linkraft/internal/api/handler"
	"github.com/noda/linkraft/internal/kvs"
)

// fakeKV は kvs.Store をそのまま使うインメモリ KV（gRPC / Raft を経由しない）。
type fakeKV struct {
	s *kvs.Store
}

func (f fakeKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := f.s.Get(key)
	return v, ok, nil
}
func (f fakeKV) Set(_ context.Context, key string, value []byte) error {
	return f.s.Set(key, value)
}
func (f fakeKV) Delete(_ context.Context, key string) error { return f.s.Delete(key) }
func (f fakeKV) Incr(_ context.Context, key string) (int64, error) {
	return f.s.Incr(key)
}
func (f fakeKV) Keys(_ context.Context, prefix string) ([]string, error) {
	return f.s.Keys(prefix), nil
}
func (f fakeKV) ClusterStatus(_ context.Context) []kvs.NodeStatus {
	return []kvs.NodeStatus{{NodeID: "node-0", Address: "fake:9000", State: "leader", LeaderID: "node-0"}}
}

var testSecret = []byte("test-secret")

func newTestServer(t *testing.T) *echo.Echo {
	t.Helper()
	h := handler.New(fakeKV{s: kvs.NewStore(nil)}, testSecret)
	return api.NewRouter(h, testSecret, "")
}

func doJSON(e *echo.Echo, method, path, body, token string) *httptest.ResponseRecorder {
	var r *strings.Reader
	if body == "" {
		r = strings.NewReader("")
	} else {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON response %q: %v", rec.Body.String(), err)
	}
	return m
}

// signup はテストユーザーを作りトークンを返す。
func signup(t *testing.T, e *echo.Echo, name, email string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"email":%q,"password":"password123"}`, name, email)
	rec := doJSON(e, http.MethodPost, "/api/auth/signup", body, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup = %d: %s", rec.Code, rec.Body.String())
	}
	return decode(t, rec)["token"].(string)
}

func TestSignupAndLogin(t *testing.T) {
	e := newTestServer(t)
	signup(t, e, "alice", "alice@example.com")

	// 重複メールは 409
	rec := doJSON(e, http.MethodPost, "/api/auth/signup",
		`{"name":"alice2","email":"alice@example.com","password":"password123"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate signup = %d, want 409", rec.Code)
	}

	// ログイン成功
	rec = doJSON(e, http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"password123"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if m["token"] == "" {
		t.Fatal("login response should contain token")
	}
	user := m["user"].(map[string]any)
	if _, hasEmail := user["email"]; hasEmail {
		t.Fatal("public user must not expose email")
	}
	if _, hasHash := user["password_hash"]; hasHash {
		t.Fatal("public user must not expose password_hash")
	}

	// パスワード誤りは 401
	rec = doJSON(e, http.MethodPost, "/api/auth/login",
		`{"email":"alice@example.com","password":"wrongpassword"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password login = %d, want 401", rec.Code)
	}
}

func TestLinkCRUDAndList(t *testing.T) {
	e := newTestServer(t)
	token := signup(t, e, "alice", "alice@example.com")

	// 未認証の投稿は 401
	rec := doJSON(e, http.MethodPost, "/api/links", `{"url":"https://example.com","title":"t"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d, want 401", rec.Code)
	}

	// 投稿
	for i := 1; i <= 3; i++ {
		body := fmt.Sprintf(`{"url":"https://example.com/%d","title":"Link %d","description":"desc","tags":["go","raft"]}`, i, i)
		rec = doJSON(e, http.MethodPost, "/api/links", body, token)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create link %d = %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// 一覧は新着順
	rec = doJSON(e, http.MethodGet, "/api/links", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	m := decode(t, rec)
	links := m["links"].([]any)
	if len(links) != 3 || m["total"].(float64) != 3 {
		t.Fatalf("list = %d links, total %v; want 3", len(links), m["total"])
	}
	if links[0].(map[string]any)["title"] != "Link 3" {
		t.Fatalf("first link = %v, want most recent (Link 3)", links[0].(map[string]any)["title"])
	}

	// タグフィルター・検索・ページネーション
	rec = doJSON(e, http.MethodGet, "/api/links?tag=go&per_page=2", "", "")
	m = decode(t, rec)
	if len(m["links"].([]any)) != 2 || m["total"].(float64) != 3 {
		t.Fatalf("tag filter: links=%d total=%v", len(m["links"].([]any)), m["total"])
	}
	rec = doJSON(e, http.MethodGet, "/api/links?q=link+2", "", "")
	m = decode(t, rec)
	if len(m["links"].([]any)) != 1 {
		t.Fatalf("search: got %d links, want 1", len(m["links"].([]any)))
	}

	// 詳細
	rec = doJSON(e, http.MethodGet, "/api/links/1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get link = %d", rec.Code)
	}

	// 他人は削除できない
	bobToken := signup(t, e, "bob", "bob@example.com")
	rec = doJSON(e, http.MethodDelete, "/api/links/1", "", bobToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete by non-owner = %d, want 403", rec.Code)
	}

	// 本人は削除できる
	rec = doJSON(e, http.MethodDelete, "/api/links/1", "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}
	rec = doJSON(e, http.MethodGet, "/api/links/1", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted link = %d, want 404", rec.Code)
	}
}

func TestVoteToggle(t *testing.T) {
	e := newTestServer(t)
	token := signup(t, e, "alice", "alice@example.com")
	doJSON(e, http.MethodPost, "/api/links", `{"url":"https://example.com","title":"t"}`, token)

	// 投票
	rec := doJSON(e, http.MethodPost, "/api/links/1/vote", "", token)
	m := decode(t, rec)
	if rec.Code != http.StatusOK || m["voted"] != true || m["vote_count"].(float64) != 1 {
		t.Fatalf("vote = %d %v", rec.Code, m)
	}
	// 取り消し
	rec = doJSON(e, http.MethodPost, "/api/links/1/vote", "", token)
	m = decode(t, rec)
	if m["voted"] != false || m["vote_count"].(float64) != 0 {
		t.Fatalf("unvote = %v", m)
	}
	// 人気順ソートの確認（bob の 1 票が入ったリンクが先頭に来る）
	bobToken := signup(t, e, "bob", "bob@example.com")
	doJSON(e, http.MethodPost, "/api/links", `{"url":"https://example.com/2","title":"t2"}`, token)
	doJSON(e, http.MethodPost, "/api/links/1/vote", "", bobToken)
	rec = doJSON(e, http.MethodGet, "/api/links?sort=popular", "", "")
	links := decode(t, rec)["links"].([]any)
	if links[0].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("popular sort: first = %v, want id 1", links[0])
	}
}

func TestComments(t *testing.T) {
	e := newTestServer(t)
	token := signup(t, e, "alice", "alice@example.com")
	doJSON(e, http.MethodPost, "/api/links", `{"url":"https://example.com","title":"t"}`, token)

	// コメント投稿
	rec := doJSON(e, http.MethodPost, "/api/links/1/comments", `{"body":"first!"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment = %d: %s", rec.Code, rec.Body.String())
	}
	// 返信（1 階層）
	rec = doJSON(e, http.MethodPost, "/api/links/1/comments", `{"body":"reply","parent_id":1}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply = %d: %s", rec.Code, rec.Body.String())
	}
	// 返信への返信は不可
	rec = doJSON(e, http.MethodPost, "/api/links/1/comments", `{"body":"nested","parent_id":2}`, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested reply = %d, want 400", rec.Code)
	}

	// 一覧
	rec = doJSON(e, http.MethodGet, "/api/links/1/comments", "", "")
	comments := decode(t, rec)["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}

	// リンクの comment_count が増えている
	rec = doJSON(e, http.MethodGet, "/api/links/1", "", "")
	link := decode(t, rec)["link"].(map[string]any)
	if link["comment_count"].(float64) != 2 {
		t.Fatalf("comment_count = %v, want 2", link["comment_count"])
	}

	// 削除
	rec = doJSON(e, http.MethodDelete, "/api/comments/2", "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete comment = %d, want 204", rec.Code)
	}
	rec = doJSON(e, http.MethodGet, "/api/links/1/comments", "", "")
	if got := len(decode(t, rec)["comments"].([]any)); got != 1 {
		t.Fatalf("comments after delete = %d, want 1", got)
	}
}

func TestUserProfileAndHealth(t *testing.T) {
	e := newTestServer(t)
	token := signup(t, e, "alice", "alice@example.com")
	doJSON(e, http.MethodPost, "/api/links", `{"url":"https://example.com","title":"t"}`, token)
	doJSON(e, http.MethodPost, "/api/links/1/vote", "", token)

	rec := doJSON(e, http.MethodGet, "/api/users/1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("profile = %d", rec.Code)
	}
	m := decode(t, rec)
	if m["total_votes"].(float64) != 1 || len(m["links"].([]any)) != 1 {
		t.Fatalf("profile = %v", m)
	}

	rec = doJSON(e, http.MethodGet, "/api/health", "", "")
	m = decode(t, rec)
	if m["leader_id"] != "node-0" {
		t.Fatalf("health leader_id = %v", m["leader_id"])
	}
}
