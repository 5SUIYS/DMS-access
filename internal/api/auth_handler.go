package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/auth"
)

// health 处理 GET /healthz 健康检查请求（无需认证）。
// 调用 deps.HealthFunc 检查依赖，返回 {"status": "ok"} 或 503。
func (h *Handler) health(c *gin.Context) {
	if h.deps.HealthFunc != nil {
		if err := h.deps.HealthFunc(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, Response{Code: "UNAVAILABLE", Message: "依赖不可用"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// me 处理 GET /api/dms/auth/me，返回当前用户的 uid 和数据库内部 ID。
// 通过 deps.UserIDFunc 将 UniAuth uid 映射为 users.id（首次登录自动创建用户）。
func (h *Handler) me(c *gin.Context) {
	uid, _ := auth.GetUID(c)
	userID := h.deps.UserIDFunc(c.Request.Context(), uid)
	writeSuccess(c, gin.H{
		"uid":     uid,
		"user_id": userID,
	})
}
