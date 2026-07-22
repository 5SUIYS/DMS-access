package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	dmssvc "github.com/5miles/dms-access/internal/dms"
	"github.com/5miles/dms-access/internal/auth"
	"github.com/5miles/dms-access/internal/domain"
)

// generatePlan 处理 POST /api/dms/tickets/:id/generate-plan。
// 调用 orchestrator.GeneratePlan 生成 DMS 执行方案。
func (h *Handler) generatePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	uid, _ := auth.GetUID(c)
	operatorID := h.deps.UserIDFunc(c.Request.Context(), uid)

	plan, err := h.deps.Orchestrator.GeneratePlan(c.Request.Context(), id, operatorID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, plan)
}

// getPlan 处理 GET /api/dms/tickets/:id/plan。
// 调用 deps.PlanRepo.GetByTicketID 查询方案。
func (h *Handler) getPlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	plan, err := h.deps.PlanRepo.GetByTicketID(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, plan)
}

// executeTicket 处理 POST /api/dms/tickets/:id/execute。
// 调用 orchestrator.Execute 异步执行 DMS 任务，返回最新工单状态。
func (h *Handler) executeTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	uid, _ := auth.GetUID(c)
	operatorID := h.deps.UserIDFunc(c.Request.Context(), uid)

	if err := h.deps.Orchestrator.Execute(c.Request.Context(), id, operatorID); err != nil {
		if errors.Is(err, dmssvc.ErrPreconditionFailed) {
			c.JSON(http.StatusUnprocessableEntity, Response{
				Code:    "PRECONDITION_FAILED",
				Message: "前置条件存在 error 级别警告，执行被阻止",
			})
			return
		}
		writeError(c, err)
		return
	}

	// 返回最新工单状态（scope=ALL，ownerID=0 跳过所有者过滤）
	t, err := h.deps.TicketSvc.GetTicket(c.Request.Context(), id, domain.DataScopeAll, 0)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, t)
}

// getAuditLogs 处理 GET /api/dms/tickets/:id/audit。
// 调用 auditSvc.ListByTicket 查询审计日志。
func (h *Handler) getAuditLogs(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}
	logs, err := h.deps.AuditSvc.ListByTicket(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, gin.H{"audit_logs": logs})
}
