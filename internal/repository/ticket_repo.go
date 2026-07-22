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

// ErrTicketNotFound 表示工单不存在。
var ErrTicketNotFound = errors.New("repository: 工单不存在")

// TicketFilter 工单列表过滤参数。
type TicketFilter struct {
	Status    string
	Scope     domain.DataScope
	OwnerID   int64 // Scope=SELF 时只查该用户的工单
}

// TicketRepository 定义工单数据访问接口。
type TicketRepository interface {
	Create(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error)
	GetByID(ctx context.Context, id int64) (*domain.Ticket, error)
	List(ctx context.Context, filter TicketFilter) ([]*domain.Ticket, error)
	// Update 仅允许 draft/rejected 状态的工单修改（Property 8）。
	Update(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error)
	UpdateStatus(ctx context.Context, id int64, status domain.TicketStatus, updatedFields map[string]interface{}) error
	UpdateDMSTask(ctx context.Context, id int64, taskARN, taskStatus string) error
}

type pgTicketRepo struct {
	pool *pgxpool.Pool
}

// NewTicketRepository 创建工单 Repository。
func NewTicketRepository(pool *pgxpool.Pool) TicketRepository {
	return &pgTicketRepo{pool: pool}
}

// Create 创建新工单。
func (r *pgTicketRepo) Create(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	selectionsJSON, err := json.Marshal(t.TableSelections)
	if err != nil {
		return nil, fmt.Errorf("repository: 序列化 table_selections 失败: %w", err)
	}

	const q = `
INSERT INTO tickets (title, status, src_datasource_id, dst_datasource_id, target_schema,
                     migration_type, table_selections, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, COALESCE(title,'') as title, status, src_datasource_id, dst_datasource_id,
          target_schema, migration_type, table_selections, COALESCE(reason,'') as reason,
          submitter_id, submitted_at, reviewer_id, reviewed_at, COALESCE(review_comment,'') as review_comment,
          executor_id, executed_at, COALESCE(dms_task_arn,'') as dms_task_arn,
          COALESCE(dms_task_status,'') as dms_task_status, COALESCE(error_detail,'') as error_detail,
          created_at, updated_at`

	row := r.pool.QueryRow(ctx, q,
		t.Title, string(t.Status), t.SrcDatasourceID, t.DstDatasourceID, t.TargetSchema,
		string(t.MigrationType), selectionsJSON, t.Reason,
	)
	result := &domain.Ticket{}
	if err := scanTicket(row, result); err != nil {
		return nil, fmt.Errorf("repository: Create ticket 失败: %w", err)
	}
	return result, nil
}

// GetByID 查询单个工单。
func (r *pgTicketRepo) GetByID(ctx context.Context, id int64) (*domain.Ticket, error) {
	const q = `
SELECT id, COALESCE(title,'') as title, status, src_datasource_id, dst_datasource_id,
       target_schema, migration_type, table_selections, COALESCE(reason,'') as reason,
       submitter_id, submitted_at, reviewer_id, reviewed_at, COALESCE(review_comment,'') as review_comment,
       executor_id, executed_at, COALESCE(dms_task_arn,'') as dms_task_arn,
       COALESCE(dms_task_status,'') as dms_task_status, COALESCE(error_detail,'') as error_detail,
       created_at, updated_at
FROM tickets WHERE id = $1`

	row := r.pool.QueryRow(ctx, q, id)
	t := &domain.Ticket{}
	if err := scanTicket(row, t); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("repository: GetByID ticket 失败: %w", err)
	}
	return t, nil
}

// List 查询工单列表，支持 self/all 数据范围过滤（需求 2.2, 2.3）。
func (r *pgTicketRepo) List(ctx context.Context, filter TicketFilter) ([]*domain.Ticket, error) {
	baseQ := `
SELECT id, COALESCE(title,'') as title, status, src_datasource_id, dst_datasource_id,
       target_schema, migration_type, table_selections, COALESCE(reason,'') as reason,
       submitter_id, submitted_at, reviewer_id, reviewed_at, COALESCE(review_comment,'') as review_comment,
       executor_id, executed_at, COALESCE(dms_task_arn,'') as dms_task_arn,
       COALESCE(dms_task_status,'') as dms_task_status, COALESCE(error_detail,'') as error_detail,
       created_at, updated_at
FROM tickets WHERE 1=1`

	args := []interface{}{}
	argIdx := 1

	if filter.Status != "" {
		baseQ += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, filter.Status)
		argIdx++
	}

	// Self 范围：只返回自己提交的工单（需求 2.2）
	if filter.Scope == domain.DataScopeSelf && filter.OwnerID > 0 {
		baseQ += fmt.Sprintf(" AND submitter_id = $%d", argIdx)
		args = append(args, filter.OwnerID)
		argIdx++
	}

	baseQ += " ORDER BY id DESC"

	rows, err := r.pool.Query(ctx, baseQ, args...)
	if err != nil {
		return nil, fmt.Errorf("repository: List tickets 失败: %w", err)
	}
	defer rows.Close()

	var result []*domain.Ticket
	for rows.Next() {
		t := &domain.Ticket{}
		if err := scanTicketFromRows(rows, t); err != nil {
			return nil, fmt.Errorf("repository: 扫描 ticket 失败: %w", err)
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// Update 更新工单内容（仅允许 draft/rejected 状态，Property 8）。
func (r *pgTicketRepo) Update(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	// 先查当前状态
	current, err := r.GetByID(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if !domain.CanModify(current.Status) {
		return nil, fmt.Errorf("%w: 工单状态 %s 不允许修改", ErrTicketNotModifiable, current.Status)
	}

	selectionsJSON, err := json.Marshal(t.TableSelections)
	if err != nil {
		return nil, fmt.Errorf("repository: 序列化 table_selections 失败: %w", err)
	}

	const q = `
UPDATE tickets
SET title=$1, src_datasource_id=$2, dst_datasource_id=$3, target_schema=$4,
    migration_type=$5, table_selections=$6, reason=$7, updated_at=NOW()
WHERE id=$8
RETURNING id, COALESCE(title,'') as title, status, src_datasource_id, dst_datasource_id,
          target_schema, migration_type, table_selections, COALESCE(reason,'') as reason,
          submitter_id, submitted_at, reviewer_id, reviewed_at, COALESCE(review_comment,'') as review_comment,
          executor_id, executed_at, COALESCE(dms_task_arn,'') as dms_task_arn,
          COALESCE(dms_task_status,'') as dms_task_status, COALESCE(error_detail,'') as error_detail,
          created_at, updated_at`

	row := r.pool.QueryRow(ctx, q,
		t.Title, t.SrcDatasourceID, t.DstDatasourceID, t.TargetSchema,
		string(t.MigrationType), selectionsJSON, t.Reason, t.ID,
	)
	result := &domain.Ticket{}
	if err := scanTicket(row, result); err != nil {
		return nil, fmt.Errorf("repository: Update ticket 失败: %w", err)
	}
	return result, nil
}

// UpdateStatus 更新工单状态及附加字段。
func (r *pgTicketRepo) UpdateStatus(ctx context.Context, id int64, status domain.TicketStatus, updatedFields map[string]interface{}) error {
	// 构造动态 SET 子句
	args := []interface{}{string(status)}
	argIdx := 2
	setClauses := "status = $1, updated_at = NOW()"

	for col, val := range updatedFields {
		setClauses += fmt.Sprintf(", %s = $%d", col, argIdx)
		args = append(args, val)
		argIdx++
	}

	args = append(args, id)
	q := fmt.Sprintf("UPDATE tickets SET %s WHERE id = $%d", setClauses, argIdx)

	_, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("repository: UpdateStatus ticket 失败: %w", err)
	}
	return nil
}

// UpdateDMSTask 更新工单的 DMS 任务 ARN 和状态。
func (r *pgTicketRepo) UpdateDMSTask(ctx context.Context, id int64, taskARN, taskStatus string) error {
	const q = `
UPDATE tickets SET dms_task_arn = $1, dms_task_status = $2, updated_at = NOW()
WHERE id = $3`
	_, err := r.pool.Exec(ctx, q, taskARN, taskStatus, id)
	if err != nil {
		return fmt.Errorf("repository: UpdateDMSTask 失败: %w", err)
	}
	return nil
}

// ErrTicketNotModifiable 表示工单当前状态不允许修改（Property 8）。
var ErrTicketNotModifiable = errors.New("repository: 工单当前状态不可修改")

// scanTicket 从 pgx Row 扫描 Ticket 字段。
func scanTicket(row pgx.Row, t *domain.Ticket) error {
	return row.Scan(
		&t.ID, &t.Title, &t.Status, &t.SrcDatasourceID, &t.DstDatasourceID,
		&t.TargetSchema, &t.MigrationType, &t.TableSelections, &t.Reason,
		&t.SubmitterID, &t.SubmittedAt,
		&t.ReviewerID, &t.ReviewedAt, &t.ReviewComment,
		&t.ExecutorID, &t.ExecutedAt,
		&t.DMSTaskARN, &t.DMSTaskStatus, &t.ErrorDetail,
		&t.CreatedAt, &t.UpdatedAt,
	)
}

// scanTicketFromRows 从 pgx Rows 扫描 Ticket 字段。
func scanTicketFromRows(rows pgx.Rows, t *domain.Ticket) error {
	return rows.Scan(
		&t.ID, &t.Title, &t.Status, &t.SrcDatasourceID, &t.DstDatasourceID,
		&t.TargetSchema, &t.MigrationType, &t.TableSelections, &t.Reason,
		&t.SubmitterID, &t.SubmittedAt,
		&t.ReviewerID, &t.ReviewedAt, &t.ReviewComment,
		&t.ExecutorID, &t.ExecutedAt,
		&t.DMSTaskARN, &t.DMSTaskStatus, &t.ErrorDetail,
		&t.CreatedAt, &t.UpdatedAt,
	)
}
