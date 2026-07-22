package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/domain"
)

// createDatasourceRequest 创建/更新数据源的请求体。
type createDatasourceRequest struct {
	Name        string `json:"name"         binding:"required"`
	Type        string `json:"type"         binding:"required,oneof=mysql redshift"`
	EndpointARN string `json:"endpoint_arn" binding:"required"`
}

// listDatasources 处理 GET /api/dms/datasources。
func (h *Handler) listDatasources(c *gin.Context) {
	items, err := h.deps.DatasourceSvc.ListDatasources(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, gin.H{"datasources": items})
}

// createDatasource 处理 POST /api/dms/datasources。
func (h *Handler) createDatasource(c *gin.Context) {
	var req createDatasourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, Response{Code: "VALIDATION_ERROR", Message: err.Error()})
		return
	}

	ds := &domain.Datasource{
		Name:        req.Name,
		Type:        req.Type,
		EndpointARN: req.EndpointARN,
	}

	result, err := h.deps.DatasourceSvc.CreateDatasource(c.Request.Context(), ds)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// getDatasource 处理 GET /api/dms/datasources/:id。
func (h *Handler) getDatasource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}
	ds, err := h.deps.DatasourceSvc.GetDatasource(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, ds)
}

// updateDatasource 处理 PUT /api/dms/datasources/:id。
func (h *Handler) updateDatasource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}

	var req createDatasourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, Response{Code: "VALIDATION_ERROR", Message: err.Error()})
		return
	}

	ds := &domain.Datasource{
		ID:          id,
		Name:        req.Name,
		Type:        req.Type,
		EndpointARN: req.EndpointARN,
	}

	result, err := h.deps.DatasourceSvc.UpdateDatasource(c.Request.Context(), ds)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, result)
}

// deleteDatasource 处理 DELETE /api/dms/datasources/:id。
// 若数据源被活跃工单引用，返回 409 DATASOURCE_IN_USE。
func (h *Handler) deleteDatasource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}
	if err := h.deps.DatasourceSvc.DeleteDatasource(c.Request.Context(), id); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// testDatasource 处理 POST /api/dms/datasources/:id/test。
// 验证 Endpoint ARN 是否有效。
func (h *Handler) testDatasource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, Response{Code: "INVALID_ID", Message: "无效的 ID"})
		return
	}
	ok, msg, err := h.deps.DatasourceSvc.TestEndpoint(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	writeSuccess(c, gin.H{"ok": ok, "message": msg})
}
