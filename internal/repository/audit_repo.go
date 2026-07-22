package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5miles/dms-access/internal/domain"
)

// AuditRepository 定义审计日志数据访问接口。
type AuditRepository interface {
	// Create 写入一条审计日志（需求 8.1）。
	Create(ctx context.Context, log *domain.AuditLog) error
	// ListByTicket 查询工单的全部审计日志（需求 8.2）。
	ListByTicket(ctx context.Context, ticketID int64) ([]*domain.AuditLog, error)
}

type pgAuditRepo struct {
	pool *pgxpool.Pool
}

// NewAuditRepository 创建审计日志 Repository。
func NewAuditRepository(pool *pgxpool.Pool) AuditRepository {
	return &pgAuditRepo{pool: pool}
}

// Create 写入审计日志，detail 中不得含 password 字段（需求 8.3，Property 17）。
func (r *pgAuditRepo) Create(ctx context.Context, log *domain.AuditLog) error {
	const q = `
INSERT INTO audit_logs (ticket_id, operator_id, action, detail)
VALUES ($1, $2, $3, $4)`

	_, err := r.pool.Exec(ctx, q, log.TicketID, log.OperatorID, log.Action, log.Detail)
	if err != nil {
		return fmt.Errorf("repository: Create audit_log 失败: %w", err)
	}
	return nil
}

// ListByTicket 查询工单的审计日志，按时间升序排列。
func (r *pgAuditRepo) ListByTicket(ctx context.Context, ticketID int64) ([]*domain.AuditLog, error) {
	const q = `
SELECT id, ticket_id, operator_id, action, detail, created_at
FROM audit_logs WHERE ticket_id = $1 ORDER BY created_at ASC`

	rows, err := r.pool.Query(ctx, q, ticketID)
	if err != nil {
		return nil, fmt.Errorf("repository: ListByTicket audit_logs 失败: %w", err)
	}
	defer rows.Close()

	var result []*domain.AuditLog
	for rows.Next() {
		log := &domain.AuditLog{}
		if err := rows.Scan(
			&log.ID, &log.TicketID, &log.OperatorID, &log.Action, &log.Detail, &log.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: 扫描 audit_log 失败: %w", err)
		}
		result = append(result, log)
	}
	return result, rows.Err()
}

// BuildAuditDetail 构造审计日志的 detail JSONB，确保不包含 password 字段（Property 17）。
func BuildAuditDetail(data map[string]interface{}) domain.JSONBMap {
	// 移除任何包含 "password" 的键（需求 8.3）
	for k := range data {
		if k == "password" || k == "password_enc" || k == "pwd" {
			delete(data, k)
		}
	}
	return domain.JSONBMap(data)
}
