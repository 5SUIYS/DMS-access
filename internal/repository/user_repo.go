// Package repository 实现 DMS-access 的数据访问层。
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5miles/dms-access/internal/domain"
)

// UserRepository 定义用户数据访问接口。
type UserRepository interface {
	// UpsertByUniauthUID 按 uniauth_uid 查找或创建用户（Property 1）。
	// 不存在则 INSERT，已存在则复用。
	UpsertByUniauthUID(ctx context.Context, uid, username, email string) (*domain.User, error)
	// GetByUniauthUID 按 uniauth_uid 查找用户。
	GetByUniauthUID(ctx context.Context, uid string) (*domain.User, error)
}

type pgUserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepository 创建用户 Repository。
func NewUserRepository(pool *pgxpool.Pool) UserRepository {
	return &pgUserRepo{pool: pool}
}

// UpsertByUniauthUID 实现 UserRepository。同一 uid 不会创建多条记录（Property 1）。
func (r *pgUserRepo) UpsertByUniauthUID(ctx context.Context, uid, username, email string) (*domain.User, error) {
	const q = `
INSERT INTO users (username, email, uniauth_uid)
VALUES ($1, $2, $3)
ON CONFLICT (uniauth_uid) DO UPDATE
  SET username   = EXCLUDED.username,
      email      = EXCLUDED.email,
      updated_at = NOW()
RETURNING id, username, email, uniauth_uid, COALESCE(perm_mask, '') as perm_mask, created_at, updated_at`

	row := r.pool.QueryRow(ctx, q, username, email, uid)
	u := &domain.User{}
	if err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.UniauthUID, &u.PermMask,
		&u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: UpsertByUniauthUID 失败: %w", err)
	}
	return u, nil
}

// GetByUniauthUID 按 uniauth_uid 查找用户。
func (r *pgUserRepo) GetByUniauthUID(ctx context.Context, uid string) (*domain.User, error) {
	const q = `
SELECT id, username, email, uniauth_uid, COALESCE(perm_mask, '') as perm_mask, created_at, updated_at
FROM users WHERE uniauth_uid = $1`

	row := r.pool.QueryRow(ctx, q, uid)
	u := &domain.User{}
	if err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.UniauthUID, &u.PermMask,
		&u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repository: GetByUniauthUID 失败: %w", err)
	}
	return u, nil
}
