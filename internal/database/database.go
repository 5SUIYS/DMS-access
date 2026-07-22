// Package database 承载 PostgreSQL 连接池初始化、健康检查与数据库迁移执行。
//
// 设计约束：
//   - DSN 与连接池参数来自 config.DatabaseConfig，DSN 仅从环境变量读取，不硬编码。
//   - 使用 pgx/v5 的 pgxpool 管理连接池。
//   - 迁移脚本以 go:embed 内嵌，启动时按版本顺序幂等执行。
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5miles/dms-access/internal/config"
)

// Connect 依据配置创建并初始化 PostgreSQL 连接池。
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: 解析 DSN 失败: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("database: 创建连接池失败: %w", err)
	}

	// 初始化后立即健康检查，连接不可用时快速失败并释放资源。
	if err := healthCheck(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// Pinger 抽象连接池的 Ping 能力，便于健康检查复用与测试替换。
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthCheck 对连接池执行一次带超时的 Ping，返回 nil 表示数据库可用。
func HealthCheck(ctx context.Context, p Pinger) error {
	return healthCheck(ctx, p)
}

func healthCheck(ctx context.Context, p Pinger) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("database: 健康检查 Ping 失败: %w", err)
	}
	return nil
}
