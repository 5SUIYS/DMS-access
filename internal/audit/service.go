// Package audit 实现审计日志服务（需求 8）。
package audit

import (
	"context"
	"fmt"

	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
)

// Service 审计服务。
type Service struct {
	repo repository.AuditRepository
}

// NewService 创建审计服务。
func NewService(repo repository.AuditRepository) *Service {
	return &Service{repo: repo}
}

// Log 写入审计日志（需求 8.1, Property 17）。
// detail 中不得含明文密码（需求 8.3）。
func (s *Service) Log(ctx context.Context, log *domain.AuditLog) error {
	if err := s.repo.Create(ctx, log); err != nil {
		return fmt.Errorf("audit: 写入审计日志失败: %w", err)
	}
	return nil
}

// ListByTicket 查询工单完整操作时间线（需求 8.2）。
func (s *Service) ListByTicket(ctx context.Context, ticketID int64) ([]*domain.AuditLog, error) {
	return s.repo.ListByTicket(ctx, ticketID)
}
