// Package ticket 实现工单 CRUD、提交、审核、驳回等服务层逻辑（需求 4, 5）。
package ticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
)

// 工单服务相关错误。
var (
	// ErrTicketNotFound 工单不存在。
	ErrTicketNotFound = repository.ErrTicketNotFound
	// ErrNotModifiable 工单当前状态不允许修改（Property 8）。
	ErrNotModifiable = repository.ErrTicketNotModifiable
	// ErrValidation 工单必填项校验失败（Property 6）。
	ErrValidation = errors.New("ticket: 工单必填项校验失败")
	// ErrRejectCommentRequired 驳回时必须填写原因（需求 5.3）。
	ErrRejectCommentRequired = errors.New("ticket: 驳回时必须填写驳回原因")
)

// Service 工单服务。
type Service struct {
	ticketRepo repository.TicketRepository
	auditRepo  repository.AuditRepository
}

// NewService 创建工单服务。
func NewService(ticketRepo repository.TicketRepository, auditRepo repository.AuditRepository) *Service {
	return &Service{ticketRepo: ticketRepo, auditRepo: auditRepo}
}

// CreateTicket 创建草稿工单（需求 4.1, 4.4）。
func (s *Service) CreateTicket(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	t.Status = domain.StatusDraft
	result, err := s.ticketRepo.Create(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("ticket: 创建工单失败: %w", err)
	}
	return result, nil
}

// GetTicket 查询工单（需求 4.4, 2.2, 2.3）。
func (s *Service) GetTicket(ctx context.Context, id int64, scope domain.DataScope, ownerID int64) (*domain.Ticket, error) {
	t, err := s.ticketRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Self 范围：只能查看自己提交的工单（需求 2.2）
	if scope == domain.DataScopeSelf && t.SubmitterID != nil && *t.SubmitterID != ownerID {
		return nil, ErrTicketNotFound
	}
	return t, nil
}

// ListTickets 查询工单列表，按数据范围过滤（需求 2.2, 2.3）。
func (s *Service) ListTickets(ctx context.Context, statusFilter string, scope domain.DataScope, ownerID int64) ([]*domain.Ticket, error) {
	filter := repository.TicketFilter{
		Status:  statusFilter,
		Scope:   scope,
		OwnerID: ownerID,
	}
	return s.ticketRepo.List(ctx, filter)
}

// UpdateTicket 更新工单内容（仅 draft/rejected 状态允许，Property 8）。
func (s *Service) UpdateTicket(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	result, err := s.ticketRepo.Update(ctx, t)
	if err != nil {
		if errors.Is(err, repository.ErrTicketNotModifiable) {
			return nil, ErrNotModifiable
		}
		return nil, err
	}
	return result, nil
}

// SubmitTicket 提交工单（draft→pending，校验必填项，需求 4.5, 4.6）。
// Property 6: 工单必填项校验
func (s *Service) SubmitTicket(ctx context.Context, id int64, submitterID int64) (*domain.Ticket, error) {
	t, err := s.ticketRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// 状态机校验（Property 9）
	if _, err := domain.ValidateTransition(t.Status, domain.ActionSubmit); err != nil {
		return nil, err
	}

	// 必填项校验（Property 6）
	if err := validateTicketFields(t); err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.ticketRepo.UpdateStatus(ctx, id, domain.StatusPending, map[string]interface{}{
		"submitter_id": submitterID,
		"submitted_at": now,
	}); err != nil {
		return nil, fmt.Errorf("ticket: 提交工单失败: %w", err)
	}

	// 写审计日志
	_ = s.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &id,
		OperatorID: &submitterID,
		Action:     domain.ActionSubmit,
	})

	return s.ticketRepo.GetByID(ctx, id)
}

// ApproveTicket 通过工单（pending→approved，需求 5.2）。
func (s *Service) ApproveTicket(ctx context.Context, id int64, reviewerID int64, comment string) (*domain.Ticket, error) {
	t, err := s.ticketRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if _, err := domain.ValidateTransition(t.Status, domain.ActionApprove); err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.ticketRepo.UpdateStatus(ctx, id, domain.StatusApproved, map[string]interface{}{
		"reviewer_id":    reviewerID,
		"reviewed_at":    now,
		"review_comment": comment,
	}); err != nil {
		return nil, fmt.Errorf("ticket: 通过工单失败: %w", err)
	}

	_ = s.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &id,
		OperatorID: &reviewerID,
		Action:     domain.ActionApprove,
		Detail:     repository.BuildAuditDetail(map[string]interface{}{"comment": comment}),
	})

	return s.ticketRepo.GetByID(ctx, id)
}

// RejectTicket 驳回工单（pending→rejected，comment 必填，需求 5.3）。
func (s *Service) RejectTicket(ctx context.Context, id int64, reviewerID int64, comment string) (*domain.Ticket, error) {
	if comment == "" {
		return nil, ErrRejectCommentRequired
	}

	t, err := s.ticketRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if _, err := domain.ValidateTransition(t.Status, domain.ActionReject); err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.ticketRepo.UpdateStatus(ctx, id, domain.StatusRejected, map[string]interface{}{
		"reviewer_id":    reviewerID,
		"reviewed_at":    now,
		"review_comment": comment,
	}); err != nil {
		return nil, fmt.Errorf("ticket: 驳回工单失败: %w", err)
	}

	_ = s.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &id,
		OperatorID: &reviewerID,
		Action:     domain.ActionReject,
		Detail:     repository.BuildAuditDetail(map[string]interface{}{"comment": comment}),
	})

	return s.ticketRepo.GetByID(ctx, id)
}

// ResubmitTicket 重新提交被驳回的工单（rejected→pending，需求 5.4）。
func (s *Service) ResubmitTicket(ctx context.Context, id int64, submitterID int64) (*domain.Ticket, error) {
	t, err := s.ticketRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if _, err := domain.ValidateTransition(t.Status, domain.ActionResubmit); err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.ticketRepo.UpdateStatus(ctx, id, domain.StatusPending, map[string]interface{}{
		"submitted_at": now,
	}); err != nil {
		return nil, fmt.Errorf("ticket: 重新提交工单失败: %w", err)
	}

	_ = s.auditRepo.Create(ctx, &domain.AuditLog{
		TicketID:   &id,
		OperatorID: &submitterID,
		Action:     domain.ActionResubmit,
	})

	return s.ticketRepo.GetByID(ctx, id)
}

// validateTicketFields 校验工单必填项（Property 6）。
func validateTicketFields(t *domain.Ticket) error {
	var missing []string
	if t.SrcDatasourceID == 0 {
		missing = append(missing, "src_datasource_id")
	}
	if t.DstDatasourceID == 0 {
		missing = append(missing, "dst_datasource_id")
	}
	if t.TargetSchema == "" {
		missing = append(missing, "target_schema")
	}
	if t.MigrationType == "" {
		missing = append(missing, "migration_type")
	}
	if len(t.TableSelections) == 0 {
		missing = append(missing, "table_selections")
	}
	if t.Reason == "" {
		missing = append(missing, "reason")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: 缺少必填字段 %v", ErrValidation, missing)
	}
	return nil
}
