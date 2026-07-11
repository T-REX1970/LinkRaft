package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/noda/linkraft/internal/kvs"
)

// KV は KVS クラスタへの操作を抽象化する。実体は *kvs.Client。
// テストではインメモリ実装に差し替える。
type KV interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	Incr(ctx context.Context, key string) (int64, error)
	Keys(ctx context.Context, prefix string) ([]string, error)
	ClusterStatus(ctx context.Context) []kvs.NodeStatus
	AddMember(ctx context.Context, id, addr string) error
	RemoveMember(ctx context.Context, id string) error
}

// ---- KVS キー構造（claude.md データ設計に対応）----

const (
	seqUser    = "seq:user"
	seqLink    = "seq:link"
	seqComment = "seq:comment"

	indexRecent = "index:links:recent"
)

func userKey(id int64) string    { return "user:" + strconv.FormatInt(id, 10) }
func linkKey(id int64) string    { return "link:" + strconv.FormatInt(id, 10) }
func commentKey(id int64) string { return "comment:" + strconv.FormatInt(id, 10) }

// emailKey はメールアドレス → user_id の逆引き用（ログインで使用）。
func emailKey(email string) string { return "user:email:" + email }

func voteKey(linkID, userID int64) string {
	return fmt.Sprintf("vote:%d:%d", linkID, userID)
}
func votePrefix(linkID int64) string {
	return fmt.Sprintf("vote:%d:", linkID)
}
func indexTag(tag string) string { return "index:links:tag:" + tag }
func indexUserLinks(userID int64) string {
	return fmt.Sprintf("index:user:%d:links", userID)
}
func indexLinkComments(linkID int64) string {
	return fmt.Sprintf("index:link:%d:comments", linkID)
}

// Repo は KV の上に JSON エンコードとインデックス操作を提供する薄い層。
// KVS にはトランザクションがないため、read-modify-write を伴う更新は
// mu で直列化する（API サーバー 1 インスタンス構成が前提）。
type Repo struct {
	kv KV
	mu sync.Mutex
}

// NewRepo は Repo を作る。
func NewRepo(kv KV) *Repo { return &Repo{kv: kv} }

// Lock / Unlock は複数キーにまたがる read-modify-write 区間を直列化する。
func (r *Repo) Lock()   { r.mu.Lock() }
func (r *Repo) Unlock() { r.mu.Unlock() }

// GetJSON は key の値を v にデコードする。キーがなければ (false, nil)。
func (r *Repo) GetJSON(ctx context.Context, key string, v any) (bool, error) {
	b, found, err := r.kv.Get(ctx, key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("unmarshal %s: %w", key, err)
	}
	return true, nil
}

// SetJSON は v を JSON にして key に保存する。
func (r *Repo) SetJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	return r.kv.Set(ctx, key, b)
}

// Index は ID リスト形式のインデックスを読む。なければ空スライス。
func (r *Repo) Index(ctx context.Context, key string) ([]int64, error) {
	var ids []int64
	if _, err := r.GetJSON(ctx, key, &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// PrependIndex はインデックスの先頭に id を追加する（新着順維持用）。
func (r *Repo) PrependIndex(ctx context.Context, key string, id int64) error {
	ids, err := r.Index(ctx, key)
	if err != nil {
		return err
	}
	return r.SetJSON(ctx, key, append([]int64{id}, ids...))
}

// AppendIndex はインデックスの末尾に id を追加する（コメントの時系列順用）。
func (r *Repo) AppendIndex(ctx context.Context, key string, id int64) error {
	ids, err := r.Index(ctx, key)
	if err != nil {
		return err
	}
	return r.SetJSON(ctx, key, append(ids, id))
}

// RemoveIndex はインデックスから id を取り除く。
func (r *Repo) RemoveIndex(ctx context.Context, key string, id int64) error {
	ids, err := r.Index(ctx, key)
	if err != nil {
		return err
	}
	out := ids[:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return r.SetJSON(ctx, key, out)
}
