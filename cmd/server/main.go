// Command dms-access 是 DMS 同步审批系统后端的入口。
//
// 启动流程（需求 9.1, 9.3, 9.4）：
//  1. 加载配置，必填环境变量缺失时打印错误列表并退出；
//  2. 连接 PostgreSQL 并执行迁移；
//  3. 构建 AWS DMS 客户端；
//  4. 装配各业务服务、路由；
//  5. 起 HTTP 服务，收到 SIGINT/SIGTERM 时优雅关闭。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/5miles/dms-access/internal/api"
	"github.com/5miles/dms-access/internal/audit"
	"github.com/5miles/dms-access/internal/auth"
	"github.com/5miles/dms-access/internal/config"
	"github.com/5miles/dms-access/internal/database"
	"github.com/5miles/dms-access/internal/datasource"
	"github.com/5miles/dms-access/internal/dms"
	"github.com/5miles/dms-access/internal/repository"
	"github.com/5miles/dms-access/internal/ticket"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("dms-access 退出", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	// 1. 加载配置（需求 9.1, 9.3）
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Info("配置加载完成",
		slog.String("server_addr", cfg.Server.Addr),
		slog.String("aws_region", cfg.DMS.Region),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 2. 数据库连接池 + 迁移（需求 9.1）
	pool, err := database.Connect(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}
	logger.Info("数据库连接与迁移完成")

	// 3. AWS DMS 客户端（需求 9.4：通过 IRSA 获取权限）
	dmsClient, err := dms.NewAWSDMSClient(ctx, cfg.DMS.Region)
	if err != nil {
		logger.Warn("创建 AWS DMS 客户端失败（本地开发模式下可忽略）", slog.String("error", err.Error()))
	}

	// 4. 构建依赖
	encKey := []byte(cfg.Encryption.Key)

	userRepo := repository.NewUserRepository(pool)
	datasourceRepo := repository.NewDatasourceRepository(pool, encKey)
	ticketRepo := repository.NewTicketRepository(pool)
	planRepo := repository.NewPlanRepository(pool)
	auditRepo := repository.NewAuditRepository(pool)

	datasourceSvc := datasource.NewService(datasourceRepo, encKey)
	auditSvc := audit.NewService(auditRepo)
	ticketSvc := ticket.NewService(ticketRepo, auditRepo)

	var orchestrator *dms.Orchestrator
	if dmsClient != nil {
		orchestrator = dms.NewOrchestrator(
			dmsClient, ticketRepo, planRepo, datasourceRepo, auditRepo,
			cfg.DMS.ReplicationInstanceARN,
		)
	}

	// 认证与鉴权
	jwks := auth.NewJWKSCache(cfg.JWKSEndpoint(), cfg.UniAuth.JWKSTimeout)
	authenticator := auth.NewAuthenticator(jwks, cfg.UniAuth.AppCode)
	resolver := auth.NewPermissionResolver(cfg.UniAuth.MyMaskURL, cfg.UniAuth.AppCode, cfg.UniAuth.PermCacheTTL)

	// UserIDFunc：将 UniAuth uid 映射到数据库内部 ID
	userIDFunc := func(ctx context.Context, uid string) int64 {
		u, err := userRepo.UpsertByUniauthUID(ctx, uid, uid, "")
		if err != nil {
			return 0
		}
		return u.ID
	}

	// 5. 构建路由
	deps := api.Deps{
		Authenticator: authenticator,
		Resolver:      resolver,
		DatasourceSvc: datasourceSvc,
		TicketSvc:     ticketSvc,
		Orchestrator:  orchestrator,
		PlanRepo:      planRepo,
		AuditSvc:      auditSvc,
		HealthFunc: func(ctx context.Context) error {
			return database.HealthCheck(ctx, pool)
		},
		UserIDFunc: userIDFunc,
	}

	router := api.NewRouter(deps)

	// 6. HTTP 服务 + 优雅关闭
	return serve(ctx, logger, cfg.Server.Addr, router)
}

func serve(ctx context.Context, logger *slog.Logger, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务启动", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("收到终止信号，开始优雅关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		logger.Info("HTTP 服务已优雅关闭")
		return nil
	}
}
