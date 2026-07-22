// Package datasource 实现数据源管理服务。
package datasource

import (
	"context"
	"errors"
	"fmt"

	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
)

// ErrDatasourceInUse 表示数据源被未完成工单引用，不可删除。
var ErrDatasourceInUse = errors.New("datasource: 数据源已被活跃工单引用，不可删除")

// Service 提供数据源管理能力。
type Service struct {
	repo repository.DatasourceRepository
}

// NewService 创建数据源服务。
func NewService(repo repository.DatasourceRepository) *Service {
	return &Service{repo: repo}
}

// CreateDatasource 创建数据源。
func (s *Service) CreateDatasource(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	result, err := s.repo.Create(ctx, ds)
	if err != nil {
		return nil, fmt.Errorf("datasource: 创建数据源失败: %w", err)
	}
	return result, nil
}

// GetDatasource 查询单个数据源。
func (s *Service) GetDatasource(ctx context.Context, id int64) (*domain.Datasource, error) {
	return s.repo.GetByID(ctx, id)
}

// ListDatasources 查询所有数据源。
func (s *Service) ListDatasources(ctx context.Context) ([]*domain.Datasource, error) {
	return s.repo.List(ctx)
}

// UpdateDatasource 更新数据源。
func (s *Service) UpdateDatasource(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	return s.repo.Update(ctx, ds)
}

// DeleteDatasource 删除数据源，被活跃工单引用时返回 ErrDatasourceInUse。
func (s *Service) DeleteDatasource(ctx context.Context, id int64) error {
	hasActive, err := s.repo.HasActiveTickets(ctx, id)
	if err != nil {
		return fmt.Errorf("datasource: 检查活跃工单失败: %w", err)
	}
	if hasActive {
		return ErrDatasourceInUse
	}
	return s.repo.Delete(ctx, id)
}

// TestEndpoint 验证数据源 Endpoint ARN 是否有效（暂未实现，后续接入 AWS SDK DescribeEndpoints）。
func (s *Service) TestEndpoint(ctx context.Context, id int64) (ok bool, message string, err error) {
	_, err = s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrDatasourceNotFound) {
			return false, "数据源不存在", nil
		}
		return false, "", err
	}
	// TODO: 调用 AWS DMS DescribeEndpoints 验证 ARN 状态
	return false, "功能暂未实现，后续将通过 AWS SDK DescribeEndpoints 验证", nil
}
