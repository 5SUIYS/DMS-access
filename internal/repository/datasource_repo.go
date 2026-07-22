package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5miles/dms-access/internal/domain"
)

// ErrDatasourceNotFound 表示数据源不存在。
var ErrDatasourceNotFound = errors.New("repository: 数据源不存在")

// DatasourceRepository 定义数据源数据访问接口。
type DatasourceRepository interface {
	Create(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error)
	GetByID(ctx context.Context, id int64) (*domain.Datasource, error)
	List(ctx context.Context) ([]*domain.Datasource, error)
	Update(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error)
	Delete(ctx context.Context, id int64) error
	// HasActiveTickets 检查数据源是否被未完成的工单引用。
	HasActiveTickets(ctx context.Context, id int64) (bool, error)
}

type pgDatasourceRepo struct {
	pool *pgxpool.Pool
}

// NewDatasourceRepository 创建数据源 Repository。
func NewDatasourceRepository(pool *pgxpool.Pool) DatasourceRepository {
	return &pgDatasourceRepo{pool: pool}
}

// Create 创建数据源。
func (r *pgDatasourceRepo) Create(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	const q = `
INSERT INTO datasources (name, type, endpoint_arn)
VALUES ($1, $2, $3)
RETURNING id, name, type, endpoint_arn, created_at, updated_at`

	row := r.pool.QueryRow(ctx, q, ds.Name, ds.Type, ds.EndpointARN)
	result := &domain.Datasource{}
	if err := scanDatasource(row, result); err != nil {
		return nil, fmt.Errorf("repository: Create datasource 失败: %w", err)
	}
	return result, nil
}

// GetByID 查询单个数据源。
func (r *pgDatasourceRepo) GetByID(ctx context.Context, id int64) (*domain.Datasource, error) {
	const q = `
SELECT id, name, type, endpoint_arn, created_at, updated_at
FROM datasources WHERE id = $1`

	row := r.pool.QueryRow(ctx, q, id)
	ds := &domain.Datasource{}
	if err := scanDatasource(row, ds); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDatasourceNotFound
		}
		return nil, fmt.Errorf("repository: GetByID datasource 失败: %w", err)
	}
	return ds, nil
}

// List 查询所有数据源。
func (r *pgDatasourceRepo) List(ctx context.Context) ([]*domain.Datasource, error) {
	const q = `
SELECT id, name, type, endpoint_arn, created_at, updated_at
FROM datasources ORDER BY id DESC`

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("repository: List datasources 失败: %w", err)
	}
	defer rows.Close()

	var result []*domain.Datasource
	for rows.Next() {
		ds := &domain.Datasource{}
		if err := scanDatasourceFromRows(rows, ds); err != nil {
			return nil, fmt.Errorf("repository: 扫描 datasource 失败: %w", err)
		}
		result = append(result, ds)
	}
	return result, rows.Err()
}

// Update 更新数据源。
func (r *pgDatasourceRepo) Update(ctx context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	const q = `
UPDATE datasources
SET name=$1, type=$2, endpoint_arn=$3, updated_at=NOW()
WHERE id=$4
RETURNING id, name, type, endpoint_arn, created_at, updated_at`

	row := r.pool.QueryRow(ctx, q, ds.Name, ds.Type, ds.EndpointARN, ds.ID)
	result := &domain.Datasource{}
	if err := scanDatasource(row, result); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDatasourceNotFound
		}
		return nil, fmt.Errorf("repository: Update datasource 失败: %w", err)
	}
	return result, nil
}

// Delete 删除数据源（调用前须检查 HasActiveTickets）。
func (r *pgDatasourceRepo) Delete(ctx context.Context, id int64) error {
	const q = `DELETE FROM datasources WHERE id = $1`
	result, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("repository: Delete datasource 失败: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrDatasourceNotFound
	}
	return nil
}

// HasActiveTickets 检查是否有未完成的工单引用该数据源。
// "未完成"定义：状态不是 completed 或 failed。
func (r *pgDatasourceRepo) HasActiveTickets(ctx context.Context, id int64) (bool, error) {
	const q = `
SELECT EXISTS(
    SELECT 1 FROM tickets
    WHERE (src_datasource_id = $1 OR dst_datasource_id = $1)
    AND status NOT IN ('completed', 'failed')
)`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("repository: HasActiveTickets 失败: %w", err)
	}
	return exists, nil
}

// scanDatasource 从 pgx Row 扫描 Datasource 字段。
func scanDatasource(row pgx.Row, ds *domain.Datasource) error {
	return row.Scan(
		&ds.ID, &ds.Name, &ds.Type, &ds.EndpointARN,
		&ds.CreatedAt, &ds.UpdatedAt,
	)
}

// scanDatasourceFromRows 从 pgx Rows 扫描 Datasource 字段。
func scanDatasourceFromRows(rows pgx.Rows, ds *domain.Datasource) error {
	return rows.Scan(
		&ds.ID, &ds.Name, &ds.Type, &ds.EndpointARN,
		&ds.CreatedAt, &ds.UpdatedAt,
	)
}
