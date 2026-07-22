// Package domain 定义 DMS-access 的核心业务模型与状态机。
package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// ─────────────────────────────────────────────
// 枚举类型与常量
// ─────────────────────────────────────────────

// TicketStatus 工单状态枚举。
type TicketStatus string

const (
	StatusDraft     TicketStatus = "draft"
	StatusPending   TicketStatus = "pending"
	StatusApproved  TicketStatus = "approved"
	StatusRejected  TicketStatus = "rejected"
	StatusPlanReady TicketStatus = "plan_ready"
	StatusExecuting TicketStatus = "executing"
	StatusCompleted TicketStatus = "completed"
	StatusFailed    TicketStatus = "failed"
)

// MigrationType 迁移类型枚举。
type MigrationType string

const (
	MigrationTypeFullLoad       MigrationType = "full-load"
	MigrationTypeCDC            MigrationType = "cdc"
	MigrationTypeFullLoadAndCDC MigrationType = "full-load-and-cdc"
)

// DataScope 数据归属范围（用于查询权限控制）。
type DataScope string

const (
	DataScopeSelf DataScope = "SELF"
	DataScopeAll  DataScope = "ALL"
)

// ─────────────────────────────────────────────
// JSONB 辅助类型
// ─────────────────────────────────────────────

// TableSelection 表示用户选择同步的单个库/表（JSONB 存储）。
type TableSelection struct {
	SchemaName string `json:"schema_name"`
	TableName  string `json:"table_name"` // 支持 "%" 通配符
}

// TableSelections 为 TableSelection 切片，实现 pgx JSONB Scanner/Valuer。
type TableSelections []TableSelection

// Scan 实现 pgx Scanner 接口，将 JSONB 数据扫描为 TableSelections。
func (ts *TableSelections) Scan(src interface{}) error {
	if src == nil {
		*ts = nil
		return nil
	}
	b, err := toBytes(src)
	if err != nil {
		return fmt.Errorf("domain: 无法将 %T 扫描为 TableSelections: %w", src, err)
	}
	return json.Unmarshal(b, ts)
}

// Value 实现 driver.Valuer 接口，将 TableSelections 序列化为 JSONB 写入数据库。
func (ts TableSelections) Value() (interface{}, error) {
	if ts == nil {
		return nil, nil
	}
	b, err := json.Marshal(ts)
	if err != nil {
		return nil, fmt.Errorf("domain: 序列化 TableSelections 失败: %w", err)
	}
	return string(b), nil
}

// PreconditionWarning 前置检查警告项（level: "error"|"warning"）。
type PreconditionWarning struct {
	Level   string `json:"level"`   // error | warning
	Item    string `json:"item"`
	Message string `json:"message"`
}

// PreconditionWarnings 为 PreconditionWarning 切片，实现 pgx JSONB Scanner/Valuer。
type PreconditionWarnings []PreconditionWarning

// Scan 实现 pgx Scanner 接口，将 JSONB 数据扫描为 PreconditionWarnings。
func (pw *PreconditionWarnings) Scan(src interface{}) error {
	if src == nil {
		*pw = nil
		return nil
	}
	b, err := toBytes(src)
	if err != nil {
		return fmt.Errorf("domain: 无法将 %T 扫描为 PreconditionWarnings: %w", src, err)
	}
	return json.Unmarshal(b, pw)
}

// Value 实现 driver.Valuer 接口，将 PreconditionWarnings 序列化为 JSONB 写入数据库。
func (pw PreconditionWarnings) Value() (interface{}, error) {
	if pw == nil {
		return nil, nil
	}
	b, err := json.Marshal(pw)
	if err != nil {
		return nil, fmt.Errorf("domain: 序列化 PreconditionWarnings 失败: %w", err)
	}
	return string(b), nil
}

// JSONBMap 通用 JSONB map 类型，用于非结构化扩展字段（如 extra_config、audit detail）。
type JSONBMap map[string]interface{}

// Scan 实现 pgx Scanner 接口。
func (m *JSONBMap) Scan(src interface{}) error {
	if src == nil {
		*m = nil
		return nil
	}
	b, err := toBytes(src)
	if err != nil {
		return fmt.Errorf("domain: 无法将 %T 扫描为 JSONBMap: %w", src, err)
	}
	return json.Unmarshal(b, m)
}

// Value 实现 driver.Valuer 接口。
func (m JSONBMap) Value() (interface{}, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("domain: 序列化 JSONBMap 失败: %w", err)
	}
	return string(b), nil
}

// toBytes 统一将 pgx 可能传入的 []byte 或 string 转换为 []byte。
func toBytes(src interface{}) ([]byte, error) {
	switch v := src.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("unsupported type %T", src)
	}
}

// ─────────────────────────────────────────────
// 核心业务结构体
// ─────────────────────────────────────────────

// User 用户记录（对应 users 表）。
type User struct {
	ID         int64     `db:"id"`
	Username   string    `db:"username"`
	Email      string    `db:"email"`
	UniauthUID string    `db:"uniauth_uid"` // UniAuth uid，首次登录自动创建
	PermMask   string    `db:"perm_mask"`   // 权限位掩码缓存（短 TTL）
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

// Datasource 数据源结构体（对应 datasources 表）。
// 简化模型：DMS endpoint 本身已含全部连接信息，本地仅存 ARN 标记。
type Datasource struct {
	ID          int64     `db:"id"`
	Name        string    `db:"name"`
	Type        string    `db:"type"`         // mysql | redshift
	EndpointARN string    `db:"endpoint_arn"` // AWS DMS Endpoint ARN（必填）
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// Ticket 工单核心结构体（对应 tickets 表）。
type Ticket struct {
	ID              int64           `db:"id"`
	Title           string          `db:"title"`
	Status          TicketStatus    `db:"status"`
	SrcDatasourceID int64           `db:"src_datasource_id"`
	DstDatasourceID int64           `db:"dst_datasource_id"`
	TargetSchema    string          `db:"target_schema"`
	MigrationType   MigrationType   `db:"migration_type"`
	TableSelections TableSelections `db:"table_selections"` // JSONB: [{schema_name, table_name}]
	Reason          string          `db:"reason"`
	SubmitterID     *int64          `db:"submitter_id"`
	SubmittedAt     *time.Time      `db:"submitted_at"`
	ReviewerID      *int64          `db:"reviewer_id"`
	ReviewedAt      *time.Time      `db:"reviewed_at"`
	ReviewComment   string          `db:"review_comment"`
	ExecutorID      *int64          `db:"executor_id"`
	ExecutedAt      *time.Time      `db:"executed_at"`
	DMSTaskARN      string          `db:"dms_task_arn"`
	DMSTaskStatus   string          `db:"dms_task_status"` // AWS 原始状态
	ErrorDetail     string          `db:"error_detail"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

// DMSPlan DMS 执行方案（对应 dms_plans 表）。
type DMSPlan struct {
	ID                     int64                `db:"id"`
	TicketID               int64                `db:"ticket_id"`
	SrcEndpointARN         string               `db:"src_endpoint_arn"`
	DstEndpointARN         string               `db:"dst_endpoint_arn"`
	ReplicationInstanceARN string               `db:"replication_instance_arn"`
	MigrationType          MigrationType        `db:"migration_type"`
	TableMappingsJSON      string               `db:"table_mappings_json"`  // 完整 DMS table-mappings JSON
	TaskSettingsJSON       string               `db:"task_settings_json"`   // DMS task settings JSON
	PreconditionWarnings   PreconditionWarnings `db:"precondition_warnings"` // JSONB 前置检查结果
	GeneratedBy            *int64               `db:"generated_by"`
	GeneratedAt            time.Time            `db:"generated_at"`
}

// AuditLog 审计日志（对应 audit_logs 表）。
type AuditLog struct {
	ID         int64     `db:"id"`
	TicketID   *int64    `db:"ticket_id"`
	OperatorID *int64    `db:"operator_id"`
	Action     string    `db:"action"` // submit | approve | reject | generate_plan | execute | status_change
	Detail     JSONBMap  `db:"detail"` // JSONB 操作附加信息，不含密码字段
	CreatedAt  time.Time `db:"created_at"`
}
