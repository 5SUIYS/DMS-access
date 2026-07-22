package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/audit"
	"github.com/5miles/dms-access/internal/auth"
	"github.com/5miles/dms-access/internal/datasource"
	dmssvc "github.com/5miles/dms-access/internal/dms"
	"github.com/5miles/dms-access/internal/repository"
	"github.com/5miles/dms-access/internal/ticket"
)

// Deps 汇聚 API 层的全部依赖。
type Deps struct {
	// 认证与鉴权
	Authenticator auth.Authenticator
	Resolver      auth.PermissionResolver

	// 业务服务
	DatasourceSvc *datasource.Service
	TicketSvc     *ticket.Service
	Orchestrator  *dmssvc.Orchestrator
	PlanRepo      repository.PlanRepository
	AuditSvc      *audit.Service

	// 健康检查
	HealthFunc func(ctx context.Context) error

	// 用户内部 ID 查询（通过 UniAuth uid 查数据库 users.id）
	UserIDFunc func(ctx context.Context, uid string) int64
}

// Handler 承载所有端点处理逻辑。
type Handler struct {
	deps Deps
}

// NewRouter 构造并返回挂载了全部端点与中间件的 Gin Engine（需求 14.1）。
//
// 路由结构：
//   - GET  /healthz                              — 健康检查（无需认证）
//   - GET  /api/dms/auth/me                      — 当前用户信息
//   - GET  /api/dms/datasources                  — 列表（dms:access）
//   - POST /api/dms/datasources                  — 创建（dms:approve）
//   - PUT  /api/dms/datasources/:id              — 更新（dms:approve）
//   - DELETE /api/dms/datasources/:id            — 删除（dms:approve）
//   - POST /api/dms/datasources/:id/test         — 连通性测试（dms:approve）
//   - GET  /api/dms/tickets                      — 列表（dms:access）
//   - POST /api/dms/tickets                      — 创建草稿（dms:apply）
//   - GET  /api/dms/tickets/:id                  — 详情（dms:access）
//   - PUT  /api/dms/tickets/:id                  — 更新（dms:apply）
//   - POST /api/dms/tickets/:id/submit           — 提交（dms:apply）
//   - POST /api/dms/tickets/:id/approve          — 通过（dms:approve）
//   - POST /api/dms/tickets/:id/reject           — 驳回（dms:approve）
//   - POST /api/dms/tickets/:id/resubmit         — 重新提交（dms:apply）
//   - POST /api/dms/tickets/:id/generate-plan    — 生成方案（dms:approve）
//   - GET  /api/dms/tickets/:id/plan             — 查看方案（dms:approve）
//   - POST /api/dms/tickets/:id/execute          — 执行（dms:approve）
//   - GET  /api/dms/tickets/:id/audit            — 审计日志（dms:access）
func NewRouter(deps Deps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(globalErrorMiddleware())

	h := &Handler{deps: deps}

	// 健康检查（无需认证）
	router.GET("/healthz", h.health)

	// 构建中间件链
	authnMW := auth.AuthMiddleware(deps.Authenticator)
	accessMW := auth.RequirePermissionWithScope(deps.Resolver, auth.PermAccess)
	applyMW := auth.RequirePermissionWithScope(deps.Resolver, auth.PermApply)
	approveMW := auth.RequirePermissionWithScope(deps.Resolver, auth.PermApprove)

	dms := router.Group("/api/dms")
	dms.Use(authnMW)

	// Auth
	dms.GET("/auth/me", accessMW, h.me)

	// 数据源管理
	dms.GET("/datasources", accessMW, h.listDatasources)
	dms.POST("/datasources", approveMW, h.createDatasource)
	dms.GET("/datasources/:id", accessMW, h.getDatasource)
	dms.PUT("/datasources/:id", approveMW, h.updateDatasource)
	dms.DELETE("/datasources/:id", approveMW, h.deleteDatasource)
	dms.POST("/datasources/:id/test", approveMW, h.testDatasource)

	// 工单管理
	dms.GET("/tickets", accessMW, h.listTickets)
	dms.POST("/tickets", applyMW, h.createTicket)
	dms.GET("/tickets/:id", accessMW, h.getTicket)
	dms.PUT("/tickets/:id", applyMW, h.updateTicket)
	dms.POST("/tickets/:id/submit", applyMW, h.submitTicket)
	dms.POST("/tickets/:id/approve", approveMW, h.approveTicket)
	dms.POST("/tickets/:id/reject", approveMW, h.rejectTicket)
	dms.POST("/tickets/:id/resubmit", applyMW, h.resubmitTicket)
	dms.POST("/tickets/:id/generate-plan", approveMW, h.generatePlan)
	dms.GET("/tickets/:id/plan", approveMW, h.getPlan)
	dms.POST("/tickets/:id/execute", approveMW, h.executeTicket)
	dms.GET("/tickets/:id/audit", accessMW, h.getAuditLogs)

	return router
}

// globalErrorMiddleware 全局错误处理中间件：记录 error 级别日志并输出统一 JSON 响应。
func globalErrorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// 检查是否有错误需要处理
		if len(c.Errors) > 0 {
			err := c.Errors.Last()
			slog.Error("请求处理错误",
				slog.String("path", c.Request.URL.Path),
				slog.String("method", c.Request.Method),
				slog.String("error", err.Error()),
			)
			if !c.Writer.Written() {
				c.JSON(http.StatusInternalServerError, Response{
					Code:    "INTERNAL_ERROR",
					Message: "服务器内部错误",
				})
			}
		}
	}
}
