package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5miles/dms-access/internal/domain"
)

// ErrPlanNotFound 表示 DMS 方案不存在。
var ErrPlanNotFound = errors.New("repository: DMS 方案不存在")

// PlanRepository 定义 DMS 方案数据访问接口。
type PlanRepository interface {
	Create(ctx context.Context, plan *domain.DMSPlan) (*domain.DMSPlan, error)
	GetByTicketID(ctx context.Context, ticketID int64) (*domain.DMSPlan, error)
}

type pgPlanRepo struct {
	pool *pgxpool.Pool
}

// NewPlanRepository 创建方案 Repository。
func NewPlanRepository(pool *pgxpool.Pool) PlanRepository {
	return &pgPlanRepo{pool: pool}
}

// Create 创建 DMS 方案。
func (r *pgPlanRepo) Create(ctx context.Context, plan *domain.DMSPlan) (*domain.DMSPlan, error) {
	warningsJSON, err := json.Marshal(plan.PreconditionWarnings)
	if err != nil {
		return nil, fmt.Errorf("repository: 序列化 precondition_warnings 失败: %w", err)
	}

	const q = `
INSERT INTO dms_plans (ticket_id, src_endpoint_arn, dst_endpoint_arn, replication_instance_arn,
                       migration_type, table_mappings_json, task_settings_json, precondition_warnings, generated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, ticket_id, src_endpoint_arn, dst_endpoint_arn, replication_instance_arn,
          migration_type, table_mappings_json, COALESCE(task_settings_json,'') as task_settings_json,
          precondition_warnings, generated_by, generated_at`

	row := r.pool.QueryRow(ctx, q,
		plan.TicketID, plan.SrcEndpointARN, plan.DstEndpointARN, plan.ReplicationInstanceARN,
		string(plan.MigrationType), plan.TableMappingsJSON, plan.TaskSettingsJSON,
		warningsJSON, plan.GeneratedBy,
	)

	result := &domain.DMSPlan{}
	if err := row.Scan(
		&result.ID, &result.TicketID, &result.SrcEndpointARN, &result.DstEndpointARN,
		&result.ReplicationInstanceARN, &result.MigrationType,
		&result.TableMappingsJSON, &result.TaskSettingsJSON,
		&result.PreconditionWarnings, &result.GeneratedBy, &result.GeneratedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: Create plan 失败: %w", err)
	}
	return result, nil
}

// GetByTicketID 按工单 ID 查询方案。
func (r *pgPlanRepo) GetByTicketID(ctx context.Context, ticketID int64) (*domain.DMSPlan, error) {
	const q = `
SELECT id, ticket_id, src_endpoint_arn, dst_endpoint_arn, replication_instance_arn,
       migration_type, table_mappings_json, COALESCE(task_settings_json,'') as task_settings_json,
       precondition_warnings, generated_by, generated_at
FROM dms_plans WHERE ticket_id = $1`

	row := r.pool.QueryRow(ctx, q, ticketID)
	result := &domain.DMSPlan{}
	if err := row.Scan(
		&result.ID, &result.TicketID, &result.SrcEndpointARN, &result.DstEndpointARN,
		&result.ReplicationInstanceARN, &result.MigrationType,
		&result.TableMappingsJSON, &result.TaskSettingsJSON,
		&result.PreconditionWarnings, &result.GeneratedBy, &result.GeneratedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("repository: GetByTicketID plan 失败: %w", err)
	}
	return result, nil
}
