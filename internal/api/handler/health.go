package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type healthNode struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	State    string `json:"state"` // leader / follower / candidate / down
	IsLeader bool   `json:"is_leader"`
}

// Health は GET /api/health。KVS クラスタの全ノード状態を返す。
func (h *Handler) Health(c echo.Context) error {
	statuses := h.repo.kv.ClusterStatus(c.Request().Context())

	nodes := make([]healthNode, 0, len(statuses))
	leaderID := ""
	for _, st := range statuses {
		nodes = append(nodes, healthNode{
			ID:       st.NodeID,
			Address:  st.Address,
			State:    st.State,
			IsLeader: st.State == "leader",
		})
		if st.State == "leader" {
			leaderID = st.NodeID
		} else if leaderID == "" && st.LeaderID != "" {
			leaderID = st.LeaderID
		}
	}
	return c.JSON(http.StatusOK, echo.Map{"nodes": nodes, "leader_id": leaderID})
}
