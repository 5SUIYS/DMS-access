// Package datasource 实现数据源管理服务（需求 3）。
package datasource

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/5miles/dms-access/internal/crypto"
	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
)

// ErrDatasourceInUse 表示数据源被未完成工单引用，不可删除（Property 5）。
var ErrDatasourceInUse = errors.New("datasource: 数据源已被活跃工单引用，不可删除")

// Service 提供数据源管理能力。
type Service struct {
	repo       repository.DatasourceRepository
	encryptKey []byte
}

// NewService 创建数据源服务。
func NewService(repo repository.DatasourceRepository, encryptKey []byte) *Service {
	return &Service{repo: repo, encryptKey: encryptKey}
}

// CreateDatasource 创建数据源，加密存储密码（需求 3.1, 3.2）。
// API 响应结构体中 password 字段始终置空（Property 3）。
func (s *Service) CreateDatasource(ctx context.Context, ds *domain.Datasource, plainPwd string) (*domain.Datasource, error) {
	result, err := s.repo.Create(ctx, ds, plainPwd)
	if err != nil {
		return nil, fmt.Errorf("datasource: 创建数据源失败: %w", err)
	}
	result.PasswordEnc = "" // Property 3
	return result, nil
}

// GetDatasource 查询单个数据源，响应中密码置空（Property 3）。
func (s *Service) GetDatasource(ctx context.Context, id int64) (*domain.Datasource, error) {
	ds, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	ds.PasswordEnc = "" // Property 3
	return ds, nil
}

// ListDatasources 查询所有数据源，响应中密码置空（Property 3）。
func (s *Service) ListDatasources(ctx context.Context) ([]*domain.Datasource, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range items {
		ds.PasswordEnc = "" // Property 3
	}
	return items, nil
}

// UpdateDatasource 更新数据源，plainPwd 非空时重新加密（需求 3.1, 3.2）。
func (s *Service) UpdateDatasource(ctx context.Context, ds *domain.Datasource, plainPwd string) (*domain.Datasource, error) {
	result, err := s.repo.Update(ctx, ds, plainPwd)
	if err != nil {
		return nil, err
	}
	result.PasswordEnc = "" // Property 3
	return result, nil
}

// DeleteDatasource 删除数据源，被活跃工单引用时返回 ErrDatasourceInUse（需求 3.5, Property 5）。
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

// TestConnectivity 测试数据源连通性（需求 3.4）：尝试 TCP 拨号。
func (s *Service) TestConnectivity(ctx context.Context, id int64) (ok bool, message string, err error) {
	ds, err := s.repo.GetByIDWithPassword(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrDatasourceNotFound) {
			return false, "数据源不存在", nil
		}
		return false, "", err
	}

	// 解密密码仅用于验证可解密（不返回给 API）
	if _, decErr := crypto.Decrypt(s.encryptKey, ds.PasswordEnc); decErr != nil {
		return false, "密码解密失败，无法测试连接", nil
	}

	addr := fmt.Sprintf("%s:%d", ds.Host, ds.Port)
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if dialErr != nil {
		return false, fmt.Sprintf("TCP 连接失败: %v", dialErr), nil
	}
	_ = conn.Close()
	return true, "连接成功", nil
}
