package dms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"

	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
)

// ErrPreconditionFailed 表示前置条件未满足，阻止执行（Property 11）。
var ErrPreconditionFailed = errors.New("dms: 前置条件存在 error 级别警告，执行被阻止")

// Orchestrator 负责 DMS 方案生成与执行编排。
type Orchestrator struct {
	client                 DMSClient
	ticketRepo             repository.TicketRepository
	planRepo               repository.PlanRepository
	datasourceRepo         repository.DatasourceRepository
	auditRepo              repository.AuditRepository
	replicationInstanceARN string
	logger                 *slog.Logger
}

// NewOrchestrator 创建 DMS 编排器。
func NewOrchestrator(
	client DMSClient,
	ticketRepo repository.TicketRepository,
	planRepo repository.PlanRepository,
	datasourceRepo repository.DatasourceRepository,
	auditRepo repository.AuditRepository,
	replicationInstanceARN string,
) *Orchestrator {
	return &Orchestrator{
		client:                 client,
		ticketRepo:             ticketRepo,
		planRepo:               planRepo,
		datasourceRepo:         datasourceRepo,
		auditRepo:              auditRepo,
		replicationInstanceARN: replicationInstanceARN,
		logger:                 slog.Default(),
	}
}

// GeneratePlan 根据工单生成 DMS 执行方案（需求 6.1, 6.2, 6.3, Property 10）。
func (o *Orchestrator) GeneratePlan(ctx context.Context, ticketID int64, operatorID int64) (*domain.DMSPlan, error) {
	ticket, err := o.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: 查询工单失败: %w", err)
	}

	// 状态校验：仅 approved 状态可生成方案
	if _, err := domain.ValidateTransition(ticket.Status, domain.ActionGeneratePlan); err != nil {
		return nil, err
	}

	srcDS, err := o.datasourceRepo.GetByID(ctx, ticket.SrcDatasourceID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: 查询源数据源失败: %w", err)
	}
	dstDS, err := o.datasourceRepo.GetByID(ctx, ticket.DstDatasourceID)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: 查询目标数据源失败: %w", err)
	}

	// 构建前置检查（endpoint ARN 缺失 → level=error）
	var warnings domain.PreconditionWarnings
	if srcDS.EndpointARN == "" {
		warnings = append(warnings, domain.PreconditionWarning{
			Level:   "error",
			Item:    "src_endpoint_arn",
			Message: "源数据源 endpoint ARN 未配置",
		})
	}
	if dstDS.EndpointARN == "" {
		warnings = append(warnings, domain.PreconditionWarning{
			Level:   "error",
			Item:    "dst_endpoint_arn",
			Message: "目标数据源 endpoint ARN 未配置",
		})
	}
	if o.replicationInstanceARN == "" {
		warnings = append(warnings, domain.PreconditionWarning{
			Level:   "error",
			Item:    "replication_instance_arn",
			Message: "复制实例 ARN 未配置",
		})
	}

	tableMappings := BuildTableMappings(ticket.TableSelections)

	opID := operatorID
	plan := &domain.DMSPlan{
		TicketID:               ticketID,
		SrcEndpointARN:         srcDS.EndpointARN,
		DstEndpointARN:         dstDS.EndpointARN,
		ReplicationInstanceARN: o.replicationInstanceARN, // Property 10
		MigrationType:          ticket.MigrationType,
		TableMappingsJSON:      tableMappings,
		PreconditionWarnings:   warnings,
		GeneratedBy:            &opID,
	}

	saved, err := o.planRepo.Create(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: 保存方案失败: %w", err)
	}

	// 更新工单状态为 plan_ready
	if err := o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusPlanReady, nil); err != nil {
		return nil, fmt.Errorf("orchestrator: 更新工单状态失败: %w", err)
	}

	// 写审计日志
	_ = o.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &ticketID,
		OperatorID: &opID,
		Action:     domain.ActionGeneratePlan,
		Detail:     repository.BuildAuditDetail(map[string]interface{}{"plan_id": saved.ID}),
	})

	return saved, nil
}

// Execute 执行 DMS 方案（需求 7.1~7.5, Property 11, 12, 13, 14）。
func (o *Orchestrator) Execute(ctx context.Context, ticketID int64, operatorID int64) error {
	ticket, err := o.ticketRepo.GetByID(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("orchestrator: 查询工单失败: %w", err)
	}

	// 状态校验（Property 9）
	if _, err := domain.ValidateTransition(ticket.Status, domain.ActionExecute); err != nil {
		return err
	}

	plan, err := o.planRepo.GetByTicketID(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("orchestrator: 查询方案失败: %w", err)
	}

	// 前置条件检查（Property 11）
	if domain.HasPreconditionErrors(plan.PreconditionWarnings) {
		return ErrPreconditionFailed
	}

	opID := operatorID

	// 更新工单状态为 executing
	now := time.Now()
	if err := o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusExecuting, map[string]interface{}{
		"executor_id": opID,
		"executed_at": now,
	}); err != nil {
		return fmt.Errorf("orchestrator: 更新工单状态失败: %w", err)
	}

	// 写审计日志
	_ = o.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &ticketID,
		OperatorID: &opID,
		Action:     domain.ActionExecute,
	})

	// 异步执行 AWS DMS API 调用
	go func() {
		bgCtx := context.Background()
		if err := o.executeAsync(bgCtx, ticket, plan, opID); err != nil {
			o.logger.Error("DMS 执行失败",
				slog.Int64("ticket_id", ticketID),
				slog.String("error", err.Error()),
			)
		}
	}()

	return nil
}

// executeAsync 异步执行 DMS 任务创建与启动（Property 12, 13, 14）。
func (o *Orchestrator) executeAsync(ctx context.Context, ticket *domain.Ticket, plan *domain.DMSPlan, operatorID int64) error {
	ticketID := ticket.ID
	taskName := fmt.Sprintf("dms-task-%d", ticketID)
	migrationType := string(plan.MigrationType)

	var taskARN string

	// 幂等检查：若 dms_task_arn 已存在则跳过 CreateReplicationTask（Property 14）
	if ticket.DMSTaskARN != "" {
		taskARN = ticket.DMSTaskARN
	} else {
		// 创建复制任务（Property 12）
		createOut, err := o.client.CreateReplicationTask(ctx, &databasemigrationservice.CreateReplicationTaskInput{
			ReplicationTaskIdentifier: &taskName,
			SourceEndpointArn:         &plan.SrcEndpointARN,
			TargetEndpointArn:         &plan.DstEndpointARN,
			ReplicationInstanceArn:    &plan.ReplicationInstanceARN,
			MigrationType:             dmstypes.MigrationTypeValue(migrationType),
			TableMappings:             &plan.TableMappingsJSON,
		})
		if err != nil {
			// 执行失败：工单状态→failed，记录错误详情（Property 15）
			errMsg := fmt.Sprintf("CreateReplicationTask 失败: %v", err)
			_ = o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusFailed, map[string]interface{}{
				"error_detail": errMsg,
			})
			_ = o.auditRepo.Create(ctx, &domain.AuditLog{
				TicketID:   &ticketID,
				OperatorID: &operatorID,
				Action:     "execute_failed",
				Detail:     repository.BuildAuditDetail(map[string]interface{}{"error": errMsg}),
			})
			return fmt.Errorf("%s", errMsg)
		}
		if createOut.ReplicationTask == nil || createOut.ReplicationTask.ReplicationTaskArn == nil {
			errMsg := "CreateReplicationTask 返回空 ARN"
			_ = o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusFailed, map[string]interface{}{
				"error_detail": errMsg,
			})
			return fmt.Errorf("%s", errMsg)
		}
		taskARN = *createOut.ReplicationTask.ReplicationTaskArn

		// 回填 Task ARN（Property 13）
		if err := o.ticketRepo.UpdateDMSTask(ctx, ticketID, taskARN, "creating"); err != nil {
			o.logger.Warn("回填 Task ARN 失败", slog.String("error", err.Error()))
		}
	}

	// 启动复制任务（Property 12）
	startType := "start-replication"
	_, err := o.client.StartReplicationTask(ctx, &databasemigrationservice.StartReplicationTaskInput{
		ReplicationTaskArn:            &taskARN,
		StartReplicationTaskType:      dmstypes.StartReplicationTaskTypeValue(startType),
	})
	if err != nil {
		errMsg := fmt.Sprintf("StartReplicationTask 失败: %v", err)
		_ = o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusFailed, map[string]interface{}{
			"error_detail": errMsg,
		})
		_ = o.auditRepo.Create(ctx, &domain.AuditLog{
			TicketID:   &ticketID,
			OperatorID: &operatorID,
			Action:     "execute_failed",
			Detail:     repository.BuildAuditDetail(map[string]interface{}{"error": errMsg}),
		})
		return fmt.Errorf("%s", errMsg)
	}

	// 启动后台轮询（Property 16）
	go o.pollTaskStatus(context.Background(), ticketID, taskARN)

	return nil
}

// pollTaskStatus 后台每 30s 轮询 DMS 任务状态（需求 7.4, Property 16）。
func (o *Orchestrator) pollTaskStatus(ctx context.Context, ticketID int64, taskARN string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			out, err := o.client.DescribeReplicationTasks(ctx, &databasemigrationservice.DescribeReplicationTasksInput{})
			if err != nil {
				o.logger.Warn("轮询 DMS 任务状态失败",
					slog.Int64("ticket_id", ticketID),
					slog.String("error", err.Error()),
				)
				continue
			}

			for _, task := range out.ReplicationTasks {
				if task.ReplicationTaskArn == nil || *task.ReplicationTaskArn != taskARN {
					continue
				}
				if task.Status == nil {
					continue
				}
				status := *task.Status

				// 更新工单 dms_task_status（Property 16）
				_ = o.ticketRepo.UpdateDMSTask(ctx, ticketID, taskARN, status)

				// 检查是否完成或失败
				switch status {
				case "load-complete", "stopped":
					_ = o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusCompleted, nil)
					return
				case "failed":
					errMsg := "DMS 任务失败"
					if task.StopReason != nil {
						errMsg = *task.StopReason
					}
					_ = o.ticketRepo.UpdateStatus(ctx, ticketID, domain.StatusFailed, map[string]interface{}{
						"error_detail": errMsg,
					})
					return
				}
			}
		}
	}
}
