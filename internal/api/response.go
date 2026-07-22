// Package api 承载 DMS-access REST API 路由、中间件与处理器。
package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/datasource"
	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
	"github.com/5miles/dms-access/internal/ticket"
)

// Response 是统一的 API 响应结构：所有接口以 {code, message, details} 返回。
type Response struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// writeSuccess 写出 200 成功响应，携带业务数据。
func writeSuccess(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Code: "SUCCESS", Message: "ok", Data: data})
}

// writeError 依据 err 的类别映射为相应 HTTP 状态码与提示写出响应。
func writeError(c *gin.Context, err error) {
	status, code, message := mapError(err)
	c.JSON(status, Response{Code: code, Message: message})
}

// mapError 将业务错误映射为 (HTTP 状态码, error code, 提示)。
func mapError(err error) (int, string, string) {
	if err == nil {
		return http.StatusOK, "SUCCESS", "success"
	}

	switch {
	case errors.Is(err, repository.ErrTicketNotFound):
		return http.StatusNotFound, "NOT_FOUND", "工单不存在"
	case errors.Is(err, repository.ErrDatasourceNotFound):
		return http.StatusNotFound, "NOT_FOUND", "数据源不存在"
	case errors.Is(err, repository.ErrPlanNotFound):
		return http.StatusNotFound, "NOT_FOUND", "方案不存在"
	case errors.Is(err, domain.ErrInvalidStateTransition):
		return http.StatusConflict, "INVALID_STATE_TRANSITION", err.Error()
	case errors.Is(err, datasource.ErrDatasourceInUse):
		return http.StatusConflict, "DATASOURCE_IN_USE", "数据源已被活跃工单引用，不可删除"
	case errors.Is(err, ticket.ErrNotModifiable):
		return http.StatusConflict, "INVALID_STATE_TRANSITION", "工单当前状态不可修改"
	case errors.Is(err, ticket.ErrValidation):
		return http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error()
	case errors.Is(err, ticket.ErrRejectCommentRequired):
		return http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error()
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误"
	}
}
