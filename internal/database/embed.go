package database

import "embed"

// migrationsFS 内嵌 migrations/ 目录下所有 .sql 文件，
// 供 Migrate 函数在不依赖外部文件系统的情况下执行迁移脚本。
//
//go:embed migrations/*.sql
var migrationsFS embed.FS
