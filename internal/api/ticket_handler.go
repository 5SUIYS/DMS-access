package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/auth"
	"github.com/5miles/dms-access/internal/domain"
)

// createTicketRequest 创建/更新工单的请求体。
type createTicketRequest struct {
	Title           string                  `json:"title"`
	SrcDatasourceID int64                   `json:"src_datasource_id" binding:"required"`
	DstDatasourceID int64                   `json:"dst_datasource_id" binding:"required"`
	TargetSchema    string                  `json:"target_schema"     binding:"required"`
	MigrationType   string                  `json:"migration_type"    binding:"required,oneof=full-load cdc full-load-and-cdc"`
	TableSelections []tableSelectionRequest `json:"table_selections"  binding:"required,min=1"`
	Reason          string                  `json:"reason"            binding:"required"`
}

// tableSelectionRequest 单个表选择项。
type tableSelectionRequest struct {
	SchemaName string `json:"schema_name" binding:"required"`
	TableName  string `json:"table_name"  binding:"required"`
}

// reviewRequest 审核操作（通过/驳回）的请求体。
type reviewRequest struct {
	Comment string `json:"comment"`
}

// listTickets 处理 GET /api/dms/tickets。
// 从 gin context 取 uid/dataScope，调用 ticketSvc.ListTickets。
func (h *Handler) listTickets(c *gin.Context) {
	statusFilter := c.Query("status")
	scope := auth.GetDataScope(c)

	uid, _ := auth.GetUID(c)
	ownerID := h.deps.UserIDFunc(c.Request.Context(), uid)

	items, err := h.deps.TicketSvc.ListTickets(c.Request.Context(), statusFilter, scope, ownerID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, gin.H{"tickets": items})
}

// createTicket 处理 POST /api/dms/tickets，创建草稿工单。
func (h *Handler) createTicket(c *gin.Context) {
	var req createTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, Response{Code: "VALIDATION_ERROR", Message: err.Error()})
		return
	}

	selections := make(domain.TableSelections, len(req.TableSelections))
	for i, s := range req.TableSelections {
		selections[i] = domain.TableSelection{SchemaName: s.SchemaName, TableName: s.TableName}
	}

	t := &domain.Ticket{
		Title:           req.Title,
		SrcDatasourceID: req.SrcDatasourceID,
		DstDatasourceID: req.DstDatasourceID,
		TargetSchema:    req.TargetSchema,
		MigrationType:   domain.MigrationType(req.MigrationType),
		TableSelections: selections,
		Reason:          req.Reason,
	}

	result, err := h.deps.TicketSvc.CreateTicket(c.Request.Context(), t)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// getTicket 处理 GET /api/dms/tickets/:id，获取工单详情。
func (h *Handler) getTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	scope := auth.GetDataScope(c)
	uid, _ := auth.GetUID(c)
	ownerID := h.deps.UserIDFunc(c.Request.Context(), uid)

	t, err := h.deps.TicketSvc.GetTicket(c.Request.Context(), id, scope, ownerID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, t)
}

// updateTicket 处理 PUT /api/dms/tickets/:id，更新草稿或被驳回状态的工单。
func (h *Handler) updateTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	var req createTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, Response{Code: "VALIDATION_ERROR", Message: err.Error()})
		return
	}

	selections := make(domain.TableSelections, len(req.TableSelections))
	for i, s := range req.TableSelections {
		selections[i] = domain.TableSelection{SchemaName: s.SchemaName, TableName: s.TableName}
	}

	t := &domain.Ticket{
		ID:              id,
		Title:           req.Title,
		SrcDatasourceID: req.SrcDatasourceID,
		DstDatasourceID: req.DstDatasourceID,
		TargetSchema:    req.TargetSchema,
		MigrationType:   domain.MigrationType(req.MigrationType),
		TableSelections: selections,
		Reason:          req.Reason,
	}

	result, err := h.deps.TicketSvc.UpdateTicket(c.Request.Context(), t)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}

// submitTicket 处理 POST /api/dms/tickets/:id/submit，提交工单（draft→pending）。
func (h *Handler) submitTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	uid, _ := auth.GetUID(c)
	submitterID := h.deps.UserIDFunc(c.Request.Context(), uid)

	result, err := h.deps.TicketSvc.SubmitTicket(c.Request.Context(), id, submitterID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}

// approveTicket 处理 POST /api/dms/tickets/:id/approve，通过审核（pending→approved）。
// body: {"comment": "..."}（可选）
func (h *Handler) approveTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	var req reviewRequest
	_ = c.ShouldBindJSON(&req)

	uid, _ := auth.GetUID(c)
	reviewerID := h.deps.UserIDFunc(c.Request.Context(), uid)

	result, err := h.deps.TicketSvc.ApproveTicket(c.Request.Context(), id, reviewerID, req.Comment)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}

// rejectTicket 处理 POST /api/dms/tickets/:id/reject，驳回工单（pending→rejected）。
// body: {"comment": "..."}（必填）
func (h *Handler) rejectTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Comment == "" {
		c.JSON(http.StatusUnprocessableEntity, Response{Code: "VALIDATION_ERROR", Message: "驳回时必须填写原因"})
		return
	}

	uid, _ := auth.GetUID(c)
	reviewerID := h.deps.UserIDFunc(c.Request.Context(), uid)

	result, err := h.deps.TicketSvc.RejectTicket(c.Request.Context(), id, reviewerID, req.Comment)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}

// resubmitTicket 处理 POST /api/dms/tickets/:id/resubmit，重新提交被驳回工单（rejected→pending）。
func (h *Handler) resubmitTicket(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	uid, _ := auth.GetUID(c)
	submitterID := h.deps.UserIDFunc(c.Request.Context(), uid)

	result, err := h.deps.TicketSvc.ResubmitTicket(c.Request.Context(), id, submitterID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}
