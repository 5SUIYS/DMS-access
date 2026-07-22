package database

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate 按版本顺序幂等地执行内嵌的升级迁移脚本。
//
// 执行器维护 schema_migrations 表记录已应用版本，仅执行尚未应用的脚本；
// 每个脚本在独立事务内执行，成功后记录版本，失败则回滚该脚本。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	files, err := migrationFiles()
	if err != nil {
		return err
	}

	for _, name := range files {
		version := migrationVersion(name)
		if applied[version] {
			continue
		}

		raw, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("database: 读取迁移脚本 %s 失败: %w", name, err)
		}

		if err := applyMigration(ctx, pool, version, name, string(raw)); err != nil {
			return err
		}
	}

	return nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version, name, script string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: 开启迁移事务失败 (%s): %w", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, script); err != nil {
		return fmt.Errorf("database: 执行迁移 %s 失败: %w", name, err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())`,
		version,
	); err != nil {
		return fmt.Errorf("database: 记录迁移版本 %s 失败: %w", version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: 提交迁移事务失败 (%s): %w", name, err)
	}
	return nil
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("database: 创建 schema_migrations 表失败: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("database: 查询已应用迁移失败: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("database: 读取迁移版本失败: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: 遍历迁移版本失败: %w", err)
	}
	return applied, nil
}

func migrationFiles() ([]string, error) {
	var files []string
	err := fs.WalkDir(migrationsFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".down.sql") {
			return nil
		}
		if strings.HasSuffix(path, ".sql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("database: 枚举迁移脚本失败: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func migrationVersion(name string) string {
	base := name
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if idx := strings.Index(base, "_"); idx >= 0 {
		return base[:idx]
	}
	return strings.TrimSuffix(base, ".sql")
}
