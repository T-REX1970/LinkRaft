package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type healthMember struct {
	ID         string `json:"id"`
	Addr       string `json:"addr"`
	MatchIndex uint64 `json:"match_index"`
}

type healthNode struct {
	ID            string `json:"id"`
	Address       string `json:"address"`
	State         string `json:"state"` // leader / follower / candidate / down
	IsLeader      bool   `json:"is_leader"`
	IsMember      bool   `json:"is_member"` // リーダーが認識する現構成に含まれるか
	Term          uint64 `json:"term"`
	CommitIndex   uint64 `json:"commit_index"`
	AppliedIndex  uint64 `json:"applied_index"`
	LastLogIndex  uint64 `json:"last_log_index"`
	SnapshotIndex uint64 `json:"snapshot_index"`
	KeysTotal     int64  `json:"keys_total"`
	MatchIndex    uint64 `json:"match_index"` // リーダーが把握している複製済みインデックス
}

// Health は GET /api/health。KVS クラスタの全ノード状態と複製の進捗を返す。
func (h *Handler) Health(c echo.Context) error {
	statuses := h.repo.kv.ClusterStatus(c.Request().Context())

	// リーダーの応答から現構成と複製進捗（matchIndex）を得る
	leaderID := ""
	var leaderTerm, leaderLastLog uint64
	matchIndex := map[string]uint64{}
	memberAddrs := map[string]string{}
	var members []healthMember
	for _, st := range statuses {
		if st.State != "leader" {
			continue
		}
		leaderID = st.NodeID
		leaderTerm = st.Term
		leaderLastLog = st.LastLogIndex
		members = make([]healthMember, 0, len(st.Members))
		for _, m := range st.Members {
			matchIndex[m.ID] = m.MatchIndex
			memberAddrs[m.ID] = m.Addr
			members = append(members, healthMember{ID: m.ID, Addr: m.Addr, MatchIndex: m.MatchIndex})
		}
		break
	}
	if leaderID == "" {
		for _, st := range statuses {
			if st.LeaderID != "" {
				leaderID = st.LeaderID
				break
			}
		}
	}

	nodes := make([]healthNode, 0, len(statuses))
	for _, st := range statuses {
		// down なノードは自分の ID を答えられないので、構成のアドレスから逆引きする
		if st.NodeID == "" {
			for mid, addr := range memberAddrs {
				if addr == st.Address {
					st.NodeID = mid
					break
				}
			}
		}
		_, isMember := memberAddrs[st.NodeID]
		if len(memberAddrs) == 0 {
			isMember = true // リーダー不明の間は構成を判定しない
		}
		nodes = append(nodes, healthNode{
			ID:            st.NodeID,
			Address:       st.Address,
			State:         st.State,
			IsLeader:      st.State == "leader",
			IsMember:      isMember,
			Term:          st.Term,
			CommitIndex:   st.CommitIndex,
			AppliedIndex:  st.AppliedIndex,
			LastLogIndex:  st.LastLogIndex,
			SnapshotIndex: st.SnapshotIndex,
			KeysTotal:     st.KeysTotal,
			MatchIndex:    matchIndex[st.NodeID],
		})
	}
	return c.JSON(http.StatusOK, echo.Map{
		"nodes":          nodes,
		"leader_id":      leaderID,
		"term":           leaderTerm,
		"last_log_index": leaderLastLog,
		"members":        members,
	})
}
