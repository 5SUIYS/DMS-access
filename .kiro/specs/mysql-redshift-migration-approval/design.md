# Design Document

> MySQL → Redshift 同步审批系统（DMS-access）

## Overview

DMS-access 是一个工单驱动的半自动化数据同步审批平台，核心目标是将"MySQL → Amazon Redshift 数据同步"这一高风险操作纳入审批流程管控，并通过系统自动编排 AWS DMS API 来消除手工操作失误。

### 核心价值

- **合规性**：所有同步操作须经运维审批，留存完整操作审计链。
- **安全性**：开发人员无需接触 AWS 凭证或数据库密码。
- **可追溯性**：每个工单的完整生命周期（提交 → 审核 → 方案生成 → 执行）均被记录。
- **半自动化**：人工确认是最后一道闸门，系统负责实际调用 AWS API。

### v1 范围边界

| 维度 | v1 决策 |
|------|---------|
| 业务类型 | 仅 MySQL → Redshift 同步 |
| 执行模式 | 半自动（运维确认后系统执行） |
| 复制实例 | 复用单个常驻 DMS 实例 |
| 端点管理 | 端点 ARN 预先手动创建，系统仅记录引用 |
| 认证 | UniAuth SSO（复用 internal 门户认证体系） |
| 前端 | 集成到 internal 统一门户（`/dms/*` 路由） |
| 部署 | DMS 后端独立部署（K8s），前端随 internal 门户部署 |


## Architecture

### 整体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│              internal 统一门户前端（Next.js，已有项目）                 │
│   /dms/* 路由模块（新增）                                              │
│   ModuleGuard(app="dms") │ TicketList │ TicketDetail │ PlanPreview  │
└───────────────────────────┬─────────────────────────────────────────┘
                            │ HTTPS / REST + JSON（/api/dms/*）
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│                  DMS-access Go / Gin API Server（独立后端）            │
│                                                                      │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐   │
│  │  Auth Layer  │  │  BizHandlers │  │  DMS Orchestrator        │   │
│  │ (JWT/UniAuth)│  │  (Ticket,    │  │  (plan generator +       │   │
│  └──────────────┘  │  Datasource, │  │   AWS SDK calls)         │   │
│                    │  Audit)      │  └──────────────────────────┘   │
│                    └──────────────┘                                  │
└──────────┬──────────────────────────────────────────┬───────────────┘
           │ PostgreSQL                                │ AWS SDK (DMS)
           ▼                                           ▼
┌─────────────────────┐                   ┌───────────────────────────┐
│    PostgreSQL DB     │                   │      AWS DMS              │
│  users / tickets /  │                   │  (pre-created endpoints + │
│  datasources /      │                   │   replication instance)   │
│  plans / audit_logs │                   └───────────────────────────┘
└─────────────────────┘
```

### 工单生命周期状态机

```
draft ──submit──► pending ──approve──► approved ──generate_plan──► plan_ready
  ▲                  │                                                    │
  │              reject│                                               confirm│
  │                  ▼                                                    ▼
  └──re-edit── rejected                                            executing
                                                                    │       │
                                                             success│   fail│
                                                                    ▼       ▼
                                                              completed   failed
                                                                          │
                                                                    retry │
                                                                          ▼
                                                                    executing
```

### 关键设计决策

1. **前端集成到 internal 统一门户**：DMS 模块作为 internal 项目的 `/dms/*` 路由，复用门户已有的 `ModuleGuard`、`PermissionGate`、`api.ts` 封装和认证体系，不单独部署前端。
2. **端点 ARN 预先登记**：DMS Endpoint 由运维手动在 AWS Console 创建后，ARN 录入数据源记录。系统创建 Task 时直接引用，避免了端点创建权限与参数校验的复杂性。
3. **table-mappings 在后端生成**：前端仅传递 schema+table 列表，后端负责拼接标准 DMS selection JSON，前端无需感知 DMS 规范细节。
4. **轮询任务状态**：Go 后端在 Task 启动后开启后台 goroutine，每 30s 调用 `DescribeReplicationTasks` 更新数据库状态，前端轮询 `/api/dms/tickets/{id}` 即可获取最新进度。
5. **密码加密**：数据源密码使用 AES-GCM（密钥从环境变量注入）加密存储，API 响应中 password 字段始终被屏蔽。


## Components and Interfaces

### 前端集成（internal 统一门户）

DMS 模块集成在 internal 项目（`/Users/5miles/github/internal`）的前端中，作为 `/dms/*` 路由下的功能模块，复用门户已有的基础设施：

**权限门禁**：复用 `ModuleGuard` 和 `PermissionGate`，权限码 `dms:access`（进入模块）、`dms:apply`（提交工单）、`dms:approve`（审核/执行）。

```tsx
// internal/web/src/app/dms/page.tsx（已存在，现为占位页）
// 替换为实际模块入口，用 ModuleGuard 包裹
<ModuleGuard app="dms">
  <DmsTicketList />
</ModuleGuard>
```

**新增页面路由**：

| 路由 | 说明 | 权限 |
|------|------|------|
| `/dms` | 工单列表（开发见自己的，运维见全部） | `dms:access` |
| `/dms/new` | 创建工单（选数据源、表、迁移类型） | `dms:apply` |
| `/dms/[id]` | 工单详情 + 操作按钮（按角色+状态显示） | `dms:access` |
| `/dms/[id]/plan` | DMS 执行方案预览 + 确认执行 | `dms:approve` |
| `/dms/admin/datasources` | 数据源 CRUD 管理 | `dms:approve` |

**API 客户端扩展**：在 `internal/web/src/lib/api.ts` 新增 DMS 相关方法（复用已有 `request` 封装），API 路径统一前缀 `/api/dms/`：

```typescript
// 在 api 对象中新增 DMS 方法
dms: {
  // 数据源
  listDatasources: (signal?: AbortSignal) =>
    request<{ datasources: DmsDatasource[] }>("/api/dms/datasources", { signal }),
  createDatasource: (input: CreateDatasourceInput) =>
    request<DmsDatasource>("/api/dms/datasources", { method: "POST", body: input }),
  testDatasource: (id: number) =>
    request<{ ok: boolean; message: string }>(`/api/dms/datasources/${id}/test`, { method: "POST" }),

  // 工单
  listTickets: (filter?: { status?: string }, signal?: AbortSignal) =>
    request<{ tickets: DmsTicket[] }>("/api/dms/tickets", { query: filter, signal }),
  createTicket: (input: CreateTicketInput) =>
    request<DmsTicket>("/api/dms/tickets", { method: "POST", body: input }),
  submitTicket: (id: number) =>
    request<DmsTicket>(`/api/dms/tickets/${id}/submit`, { method: "POST" }),
  approveTicket: (id: number, comment: string) =>
    request<DmsTicket>(`/api/dms/tickets/${id}/approve`, { method: "POST", body: { comment } }),
  rejectTicket: (id: number, comment: string) =>
    request<DmsTicket>(`/api/dms/tickets/${id}/reject`, { method: "POST", body: { comment } }),
  generatePlan: (id: number) =>
    request<DmsPlan>(`/api/dms/tickets/${id}/generate-plan`, { method: "POST" }),
  executeTicket: (id: number) =>
    request<DmsTicket>(`/api/dms/tickets/${id}/execute`, { method: "POST" }),
}
```

**类型定义**：在 `internal/web/src/types/index.ts` 新增 DMS 相关类型，`AppName` 中 `dms` 已存在，新增：

```typescript
export interface DmsDatasource {
  id: number;
  name: string;
  type: "mysql" | "redshift";
  env: "test" | "prod";
  host: string;
  port: number;
  database_name: string;
  endpoint_arn?: string;
  created_at: string;
}

export type DmsTicketStatus =
  | "draft" | "pending" | "approved" | "rejected"
  | "plan_ready" | "executing" | "completed" | "failed";

export interface DmsTicket {
  id: number;
  status: DmsTicketStatus;
  src_datasource_id: number;
  dst_datasource_id: number;
  target_schema: string;
  migration_type: "full-load" | "cdc" | "full-load-and-cdc";
  table_selections: Array<{ schema_name: string; table_name: string }>;
  reason: string;
  dms_task_arn?: string;
  dms_task_status?: string;
  error_detail?: string;
  created_at: string;
}
```

**前端错误处理**：复用门户已有的 `ApiError` 和 Toast 通知，工单执行中状态下前端每 10s 轮询一次工单详情，展示最新 `dms_task_status`。

### 后端 API（Go / Gin）

所有路径统一使用 `/api/dms/` 前缀，与 internal 门户 BFF 的路由转发约定对齐。

#### 认证

认证复用 internal 门户的 UniAuth JWT 体系。门户前端携带 UniAuth Bearer token 请求后端，后端本地验签取 uid，按 `dms` appCode 校验权限位。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/dms/auth/me` | 返回当前用户信息与 dms 权限位 |

#### 数据源

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | `/api/dms/datasources` | `dms:access` | 列表（隐藏密码），开发只见 name/type/env |
| POST | `/api/dms/datasources` | `dms:approve` | 创建数据源 |
| PUT | `/api/dms/datasources/:id` | `dms:approve` | 更新数据源 |
| DELETE | `/api/dms/datasources/:id` | `dms:approve` | 删除（检查引用） |
| POST | `/api/dms/datasources/:id/test` | `dms:approve` | 测试连通性 |

#### 工单

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | `/api/dms/tickets` | `dms:access` | 列表（开发 self / 运维 all） |
| POST | `/api/dms/tickets` | `dms:apply` | 创建草稿 |
| GET | `/api/dms/tickets/:id` | `dms:access` | 详情 |
| PUT | `/api/dms/tickets/:id` | `dms:apply` | 编辑（仅 draft/rejected） |
| POST | `/api/dms/tickets/:id/submit` | `dms:apply` | 提交（draft→pending） |
| POST | `/api/dms/tickets/:id/approve` | `dms:approve` | 通过（pending→approved） |
| POST | `/api/dms/tickets/:id/reject` | `dms:approve` | 驳回（pending→rejected） |
| POST | `/api/dms/tickets/:id/generate-plan` | `dms:approve` | 生成方案（approved→plan_ready） |
| GET | `/api/dms/tickets/:id/plan` | `dms:approve` | 获取执行方案预览 |
| POST | `/api/dms/tickets/:id/execute` | `dms:approve` | 确认执行（plan_ready→executing） |
| GET | `/api/dms/tickets/:id/audit` | `dms:access` | 审计日志时间线 |


### DMS Orchestrator（后端内部模块）

```go
// 核心接口
type DMSOrchestrator interface {
    GeneratePlan(ticket *Ticket, srcDS *Datasource, dstDS *Datasource) (*DMSPlan, error)
    Execute(plan *DMSPlan) (*ExecutionResult, error)
    PollTaskStatus(taskARN string) (TaskStatus, error)
}
```

**GeneratePlan 流程：**
1. 从 `srcDS.EndpointARN` 和 `dstDS.EndpointARN` 读取预创建端点 ARN。
2. 构建 `table-mappings` JSON（selection rules）。
3. 组装 `CreateReplicationTaskInput`（引用常驻实例 ARN、迁移类型、task settings）。
4. 将方案序列化存入 `dms_plans` 表，工单状态流转为 `plan_ready`。

**Execute 流程：**
1. 读取 `dms_plans` 中已生成的方案。
2. 调用 `dms.CreateReplicationTask`，获取 Task ARN 回填到工单。
3. 调用 `dms.StartReplicationTask`（StartReplicationTaskType: `start-replication`）。
4. 启动后台 goroutine 轮询任务状态，写入 `tickets.dms_task_status`。

**重试保护**：Execute 前检查 `tickets.dms_task_arn` 是否已存在，已存在则跳过创建，直接尝试 Start，防止重复创建资源。


## Data Models

### PostgreSQL Schema

#### `users` 表

用户记录在首次 UniAuth 登录时自动创建，通过 `uniauth_uid` 与 UniAuth 身份绑定。权限（`dms:apply`、`dms:approve`）由 UniAuth 统一管理，本表只存基础信息与权限位缓存（短 TTL）。

```sql
CREATE TABLE users (
    id          BIGSERIAL PRIMARY KEY,
    username    VARCHAR(64)  NOT NULL UNIQUE,
    email       VARCHAR(128),
    uniauth_uid VARCHAR(128) NOT NULL UNIQUE, -- UniAuth uid，首次登录自动创建
    perm_mask   VARCHAR(256),                 -- 权限位掩码缓存（短 TTL，仅做日志关联用）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### `datasources` 表

```sql
CREATE TABLE datasources (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    type            VARCHAR(32)  NOT NULL, -- mysql | redshift
    env             VARCHAR(32)  NOT NULL, -- test | prod
    host            VARCHAR(256) NOT NULL,
    port            INT          NOT NULL,
    database_name   VARCHAR(128),
    username        VARCHAR(128) NOT NULL,
    password_enc    TEXT         NOT NULL, -- AES-GCM 加密
    region          VARCHAR(64),
    endpoint_arn    VARCHAR(512),          -- 预创建端点 ARN
    extra_config    JSONB,                 -- 扩展配置（SSL 等）
    created_by      BIGINT REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### `tickets` 表

```sql
CREATE TABLE tickets (
    id                  BIGSERIAL PRIMARY KEY,
    title               VARCHAR(256),
    status              VARCHAR(32) NOT NULL DEFAULT 'draft',
    -- draft | pending | approved | rejected | plan_ready | executing | completed | failed
    src_datasource_id   BIGINT NOT NULL REFERENCES datasources(id),
    dst_datasource_id   BIGINT NOT NULL REFERENCES datasources(id),
    target_schema       VARCHAR(128) NOT NULL,
    migration_type      VARCHAR(32)  NOT NULL, -- full-load | cdc | full-load-and-cdc
    table_selections    JSONB        NOT NULL, -- [{schema, table}] 用户选择列表
    reason              TEXT,
    submitter_id        BIGINT REFERENCES users(id),
    submitted_at        TIMESTAMPTZ,
    reviewer_id         BIGINT REFERENCES users(id),
    reviewed_at         TIMESTAMPTZ,
    review_comment      TEXT,
    executor_id         BIGINT REFERENCES users(id),
    executed_at         TIMESTAMPTZ,
    dms_task_arn        VARCHAR(512),
    dms_task_status     VARCHAR(64),   -- AWS 原始状态
    error_detail        TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### `dms_plans` 表

```sql
CREATE TABLE dms_plans (
    id                      BIGSERIAL PRIMARY KEY,
    ticket_id               BIGINT NOT NULL UNIQUE REFERENCES tickets(id),
    src_endpoint_arn        VARCHAR(512) NOT NULL,
    dst_endpoint_arn        VARCHAR(512) NOT NULL,
    replication_instance_arn VARCHAR(512) NOT NULL,
    migration_type          VARCHAR(32)  NOT NULL,
    table_mappings_json     TEXT         NOT NULL, -- 完整 DMS table-mappings JSON
    task_settings_json      TEXT,                  -- DMS task settings JSON
    precondition_warnings   JSONB,                 -- 前置检查结果
    generated_by            BIGINT REFERENCES users(id),
    generated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

#### `audit_logs` 表

```sql
CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   BIGINT REFERENCES tickets(id),
    operator_id BIGINT REFERENCES users(id),
    action      VARCHAR(64)  NOT NULL, -- submit | approve | reject | generate_plan | execute | status_change
    detail      JSONB,                 -- 操作附加信息（审核意见、状态变更前后值等）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_logs_ticket_id ON audit_logs(ticket_id);
```


### 核心 Go 结构体

```go
// Ticket 工单核心结构
type Ticket struct {
    ID               int64            `db:"id"`
    Status           TicketStatus     `db:"status"`
    SrcDatasourceID  int64            `db:"src_datasource_id"`
    DstDatasourceID  int64            `db:"dst_datasource_id"`
    TargetSchema     string           `db:"target_schema"`
    MigrationType    MigrationType    `db:"migration_type"`
    TableSelections  []TableSelection `db:"table_selections"` // JSONB
    Reason           string           `db:"reason"`
    DMSTaskARN       string           `db:"dms_task_arn"`
    DMSTaskStatus    string           `db:"dms_task_status"`
    ErrorDetail      string           `db:"error_detail"`
    // ...timestamps and user refs
}

type TableSelection struct {
    SchemaName string `json:"schema_name"`
    TableName  string `json:"table_name"` // 支持 "%" 通配
}

// DMSPlan DMS 执行方案
type DMSPlan struct {
    ID                    int64  `db:"id"`
    TicketID              int64  `db:"ticket_id"`
    SrcEndpointARN        string `db:"src_endpoint_arn"`
    DstEndpointARN        string `db:"dst_endpoint_arn"`
    ReplicationInstanceARN string `db:"replication_instance_arn"`
    TableMappingsJSON     string `db:"table_mappings_json"`
    TaskSettingsJSON      string `db:"task_settings_json"`
    PreconditionWarnings  []PreconditionWarning `db:"precondition_warnings"`
}

type PreconditionWarning struct {
    Level   string `json:"level"`   // error | warning
    Item    string `json:"item"`
    Message string `json:"message"`
}
```

### table-mappings JSON 生成规则

后端根据 `ticket.TableSelections` 按如下规则生成标准 DMS selection JSON：

```go
func BuildTableMappings(selections []TableSelection) string {
    rules := make([]map[string]interface{}, 0, len(selections))
    for i, sel := range selections {
        rules = append(rules, map[string]interface{}{
            "rule-id":     strconv.Itoa(i + 1),
            "rule-name":   fmt.Sprintf("sync-rule-%d", i+1),
            "rule-type":   "selection",
            "rule-action": "include",
            "object-locator": map[string]string{
                "schema-name": sel.SchemaName,
                "table-name":  sel.TableName, // 用户可填 "%" 以匹配全部表
            },
            "filters": []interface{}{},
        })
    }
    result, _ := json.Marshal(map[string]interface{}{"rules": rules})
    return string(result)
}
```


## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system — essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

**Property Reflection（冗余消除）：**

- 2.2 和 2.3 可合并为统一的"角色权限隔离"属性。
- 2.5 和 2.6 可合并为"未授权请求均返回 403"。
- 3.2 和 3.3 合并为"密码不泄露"属性。
- 5.5 和 4.6 合并为"状态机非法转换拦截"。
- 7.3 和 7.7（幂等性）是独立的，保留。
- 8.1 和 8.2 合并为"审计完整性"属性。

---

### Property 1: UniAuth 首次登录自动创建账号（幂等）

*For any* 有效的 UniAuth token，系统解析 uid 后：若用户不存在则创建，若用户已存在则复用；最终 `users.uniauth_uid = token.uid` 恒成立，且同一 uid 不会创建多个用户记录。

**Validates: Requirements 1.3**

---

### Property 2: 角色权限隔离

*For any* 用户（developer 或 ops 角色）和系统中的任意 API 端点，该用户对该端点的请求结果（允许/拒绝）应与其角色对应的权限矩阵完全一致：developer 访问 ops-only 端点返回 403，ops 访问 developer-only 资源不受额外限制。

**Validates: Requirements 2.1, 2.2, 2.3, 2.5, 2.6**

---

### Property 3: 密码零泄露

*For any* 数据源（无论密码内容如何），通过任意 API 端点（列表、详情、工单选择器）返回的响应体中，均不应包含明文密码字段。

**Validates: Requirements 3.2, 3.3**

---

### Property 4: 数据源创建读取一致性

*For any* 合法的数据源输入（含任意 host、port、名称、endpoint_arn），创建后通过 GET 接口读取，所有非密码字段的值应与创建时输入的值完全相同（round-trip）。

**Validates: Requirements 3.1**

---

### Property 5: 被引用数据源不可删除

*For any* 数据源，若存在至少一个状态不是 `completed` 或 `failed`（即未终止）的工单引用它，则对该数据源的 DELETE 请求应返回错误。

**Validates: Requirements 3.5**

---

### Property 6: 工单必填项校验

*For any* 缺少任意必填字段（源/目标数据源、target_schema、migration_type、table_selections、reason）的工单提交请求，系统应返回 4xx 验证错误，不创建或变更工单状态。

**Validates: Requirements 4.1, 4.5**

---

### Property 7: table-mappings 生成正确性

*For any* 非空的表选择列表（`[]TableSelection`），`BuildTableMappings` 生成的 JSON 应满足：
1. 每条 `TableSelection` 对应恰好一条 `rule-type = "selection"` 的 inclusion 规则；
2. 不包含任何额外的规则；
3. 每条规则的 `object-locator.schema-name` 和 `table-name` 与输入完全一致。

**Validates: Requirements 4.2**

---

### Property 8: 非草稿/驳回工单不可修改

*For any* 处于 `pending`、`approved`、`plan_ready`、`executing`、`completed`、`failed` 状态的工单，任何 PUT/PATCH 修改请求均应被拒绝并返回错误。

**Validates: Requirements 4.6**

---

### Property 9: 状态机非法转换拦截

*For any* 工单，以下操作只在对应状态下合法，其余均应返回错误：
- `approve` / `reject` 仅在 `pending` 状态合法；
- `generate-plan` 仅在 `approved` 状态合法；
- `execute` 仅在 `plan_ready` 状态合法；
- `resubmit` 仅在 `rejected` 状态合法。

**Validates: Requirements 5.5, 5.4**

---

### Property 10: 方案引用正确的复制实例

*For any* 通过 `generate-plan` 生成的 DMS 方案，`dms_plans.replication_instance_arn` 应等于环境变量 `DMS_REPLICATION_INSTANCE_ARN` 的值。

**Validates: Requirements 6.2**

---

### Property 11: 前置条件缺失时阻止执行

*For any* 存在前置条件错误（`precondition_warnings` 中含 `level = "error"` 的条目）的方案，调用 `execute` 应被拒绝。

**Validates: Requirements 6.4**

---

### Property 12: 执行 API 调用顺序与参数正确性

*For any* 有效的 `DMSPlan`，调用 `Execute` 时（使用 mock AWS SDK）应按序触发：
1. `CreateReplicationTask`，参数中 `SourceEndpointArn`、`TargetEndpointArn`、`ReplicationInstanceArn`、`TableMappings` 均来自该方案；
2. `StartReplicationTask`，参数为上一步返回的 `ReplicationTaskArn`。

**Validates: Requirements 7.1**

---

### Property 13: 执行结果 ARN 回填

*For any* mock AWS DMS 返回的 `ReplicationTaskArn` 值，执行完成后读取对应工单，`dms_task_arn` 应等于该返回值。

**Validates: Requirements 7.3**

---

### Property 14: 执行幂等性（重试不重复创建）

*For any* 已有 `dms_task_arn` 的工单再次调用执行，系统不应调用 `CreateReplicationTask`，只应调用 `StartReplicationTask`。

**Validates: Requirements 7.7**

---

### Property 15: 执行失败保留错误详情

*For any* 在执行流程中发生的 AWS API 错误，工单状态应变为 `failed`，且 `error_detail` 字段应包含该错误信息（非空）。

**Validates: Requirements 7.6**

---

### Property 16: 任务状态轮询一致性

*For any* DMS 任务状态值（如 `running`、`load-complete`、`stopped`），轮询后写入的 `tickets.dms_task_status` 应与 AWS 返回值一致。

**Validates: Requirements 7.4**

---

### Property 17: 审计日志完整性与无密码

*For any* 在工单上执行的关键操作（submit、approve、reject、generate_plan、execute），系统应生成一条 `audit_logs` 记录，该记录包含正确的 `ticket_id`、`operator_id`、`action`，且 `detail` JSONB 不含任何以 `password` 为键的非空值。

**Validates: Requirements 8.1, 8.2, 8.3**

---

### Property 18: 环境变量缺失启动失败

*For any* 必填环境变量的缺失子集，配置校验函数应返回包含每个缺失变量名称的错误列表（非静默通过）。

**Validates: Requirements 9.3**


## Error Handling

### 错误码规范

后端统一返回以下结构：

```json
{
  "code": "TICKET_NOT_FOUND",
  "message": "工单不存在",
  "details": {}
}
```

| 场景 | HTTP 状态码 | code |
|------|-------------|------|
| 未认证 | 401 | `UNAUTHORIZED` |
| 无权限 | 403 | `FORBIDDEN` |
| 资源不存在 | 404 | `NOT_FOUND` |
| 状态机非法转换 | 409 | `INVALID_STATE_TRANSITION` |
| 数据源被引用不可删 | 409 | `DATASOURCE_IN_USE` |
| 请求参数校验失败 | 422 | `VALIDATION_ERROR` |
| AWS DMS API 调用失败 | 500 | `AWS_API_ERROR` |
| 前置条件未满足 | 422 | `PRECONDITION_FAILED` |

### 分层错误处理策略

```
HTTP Handler
    ├─ 输入校验（binding tag / custom validator）
    │       └─ 422 VALIDATION_ERROR
    ├─ 服务层调用
    │       ├─ 业务规则错误（状态机、权限）→ 409/403
    │       └─ 基础设施错误（DB/AWS）→ 包装为 AppError 后向上抛
    └─ 全局错误中间件：
            ├─ 记录 error level 日志（含 trace_id）
            └─ 序列化为统一 JSON 响应
```

### AWS DMS 执行失败恢复

执行流程中任一步骤失败时：
1. 记录当前已成功的资源 ARN（task ARN 若已创建则保留）。
2. 工单状态设为 `failed`，`error_detail` 写入 AWS 错误消息。
3. 写入 `audit_logs`（action: `execute_failed`，detail 含错误信息）。
4. 暴露"重试"按钮，重试时检查已有资源，跳过已创建步骤。

### 前端错误处理

- API 错误统一由 SWR fetcher 捕获，非 2xx 均 throw Error。
- Toast 通知展示 `message` 字段内容。
- 工单执行中状态下前端每 10s 轮询一次工单详情，展示最新 `dms_task_status`。


## Testing Strategy

### 测试分层

| 层次 | 工具 | 覆盖范围 |
|------|------|---------|
| 单元测试 | Go `testing` + `testify` | 纯函数：`BuildTableMappings`、权限解码、状态机转换、配置校验 |
| 属性测试 | `gopter`（Go PBT 库） | 本文档中所有 Correctness Properties |
| 集成测试 | `testcontainers-go`（PostgreSQL） | 数据库 CRUD、事务、审计日志 |
| API 测试 | `httptest` + mock AWS SDK | HTTP 路由、认证中间件、端到端工单流程 |
| 前端组件测试 | Vitest + React Testing Library（internal 项目已配置） | DMS 模块的表单校验、状态渲染、权限按钮 |
| E2E 测试（可选） | Playwright | 完整工单审批流程冒烟测试 |

### 属性测试配置（gopter）

```go
// 每条属性测试至少运行 100 次随机输入
properties := gopter.NewProperties(gopter.DefaultTestParameters())
// DefaultTestParameters().MinSuccessfulTests = 100

// 示例：Property 7 table-mappings 生成正确性
properties.Property("BuildTableMappings round-trip",
    // Feature: mysql-redshift-migration-approval, Property 7: table-mappings 生成正确性
    prop.ForAll(
        func(selections []TableSelection) bool {
            if len(selections) == 0 {
                return true
            }
            result := BuildTableMappings(selections)
            var out map[string]interface{}
            if err := json.Unmarshal([]byte(result), &out); err != nil {
                return false
            }
            rules := out["rules"].([]interface{})
            if len(rules) != len(selections) {
                return false
            }
            // 验证每条 selection 都出现在 rules 中
            for i, sel := range selections {
                rule := rules[i].(map[string]interface{})
                loc := rule["object-locator"].(map[string]interface{})
                if loc["schema-name"] != sel.SchemaName || loc["table-name"] != sel.TableName {
                    return false
                }
            }
            return true
        },
        genTableSelections(), // 自定义 gopter 生成器
    ))
```

每个属性测试的注释标签格式：

```go
// Feature: mysql-redshift-migration-approval, Property N: <property_text>
```

### Mock AWS SDK

使用接口注入实现 AWS SDK mock，避免真实 AWS 调用：

```go
type DMSClient interface {
    CreateReplicationTask(ctx context.Context, input *dms.CreateReplicationTaskInput, ...) (*dms.CreateReplicationTaskOutput, error)
    StartReplicationTask(ctx context.Context, input *dms.StartReplicationTaskInput, ...) (*dms.StartReplicationTaskOutput, error)
    DescribeReplicationTasks(ctx context.Context, input *dms.DescribeReplicationTasksInput, ...) (*dms.DescribeReplicationTasksOutput, error)
}
// 测试时注入 MockDMSClient，生产代码使用 aws-sdk-go-v2 实现
```

### 测试优先级

1. **必须完成**：属性测试（Properties 1–18）+ 状态机转换单元测试 + API 认证中间件测试。
2. **重要**：数据源 CRUD 集成测试、密码加密验证、审计日志完整性。
3. **推荐**：前端组件测试（角色权限按钮、工单表单校验）。
4. **可选**：E2E 冒烟测试。

### 集成测试环境变量

```bash
TEST_DB_URL=postgres://user:pass@localhost:5432/dms_access_test
DMS_REPLICATION_INSTANCE_ARN=arn:aws:dms:us-east-1:123456789:rep:test-instance
ENCRYPTION_KEY=test-key-32-bytes-long-padding!!
UNIAUTH_ENABLE=false
```


---

## 附录 A：AWS 前置依赖检查清单

在部署系统之前，运维需确认以下 AWS 资源已就位：

| 资源 | 要求 | 备注 |
|------|------|------|
| DMS Replication Instance | 常驻实例，状态 available | ARN 写入 `DMS_REPLICATION_INSTANCE_ARN` 环境变量 |
| Source Endpoint（MySQL） | 已在 DMS Console 创建，状态 successful | ARN 录入 datasource.endpoint_arn |
| Target Endpoint（Redshift） | 已在 DMS Console 创建，状态 successful | ARN 录入 datasource.endpoint_arn |
| IAM Role: `dms-vpc-role` | AmazonDMSVPCManagementRole managed policy | DMS 需要此角色访问 VPC |
| IRSA/节点 IAM 角色 | 包含 `dms:CreateReplicationTask`、`dms:StartReplicationTask`、`dms:DescribeReplicationTasks` 权限 | 不使用长期 Access Key |
| MySQL binlog（CDC 场景） | `binlog_format=ROW`，`binlog_row_image=FULL` | 系统在方案生成时可选校验 |

## 附录 B：关键环境变量列表

**DMS-access 后端（独立部署）：**

| 变量名 | 必填 | 示例值 | 说明 |
|--------|------|--------|------|
| `DATABASE_URL` | ✅ | `postgres://...` | PostgreSQL 连接串 |
| `JWT_SECRET` | ✅ | `<32+ chars>` | JWT 签名密钥（需与 internal 门户共用同一套 UniAuth JWKS） |
| `ENCRYPTION_KEY` | ✅ | `<32 bytes hex>` | 数据源密码 AES-GCM 密钥 |
| `DMS_REPLICATION_INSTANCE_ARN` | ✅ | `arn:aws:dms:...` | 常驻复制实例 ARN |
| `AWS_REGION` | ✅ | `ap-east-1` | AWS 区域 |
| `UNIAUTH_URL` | ✅ | `https://uniauth.example.com` | UniAuth 服务地址（用于 JWKS 验签） |
| `UNIAUTH_APP_CODE` | ✅ | `dms` | DMS 在 UniAuth 注册的 appCode |
| `UNIAUTH_MYMASK_URL` | ✅ | `https://...` | 权限掩码查询接口 |

**internal 门户前端（已有，需确认 DMS 相关配置）：**

| 变量名 | 说明 |
|--------|------|
| `NEXT_PUBLIC_API_BASE` | 门户 BFF 基础地址，`/api/dms/*` 经此转发到 DMS 后端 |

