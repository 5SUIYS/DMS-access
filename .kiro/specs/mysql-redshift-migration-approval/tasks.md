# Implementation Plan: DMS-access 后端（MySQL → Redshift 同步审批系统）

## Overview

基于需求文档和设计文档，实现 DMS-access 独立后端服务（Go / Gin）。采用与 internal 项目相同的分层架构：config → database → domain → repository → service → api。**本任务列表仅包含后端 Go 实现**，不含任何前端页面、组件或 TypeScript 类型定义。

## Tasks

- [x] 1. 初始化项目结构与基础设施
  - 创建 `go.mod`（模块名 `github.com/5miles/dms-access`），引入依赖：`gin-gonic/gin`、`jackc/pgx/v5`、`aws-sdk-go-v2/service/databasemigrationservice`、`leanovate/gopter`、`stretchr/testify`
  - 创建目录结构：`cmd/server/`、`internal/config/`、`internal/database/`、`internal/domain/`、`internal/crypto/`、`internal/auth/`、`internal/repository/`、`internal/ticket/`、`internal/datasource/`、`internal/dms/`、`internal/audit/`、`internal/api/`
  - 创建 `cmd/server/main.go` 占位入口，创建 `Makefile`（run / test / build 目标）
  - _Requirements: 9.1, 9.2_

- [x] 2. 实现配置加载与必填环境变量校验
  - [x] 2.1 创建 `internal/config/config.go`：定义 `Config` 结构体（Server/Database/UniAuth/DMS/Encryption 字段），实现 `Load()` 从环境变量读取全部配置，实现 `Validate()` 校验必填项（DATABASE_URL、JWT_SECRET、ENCRYPTION_KEY、DMS_REPLICATION_INSTANCE_ARN、AWS_REGION、UNIAUTH_URL、UNIAUTH_APP_CODE、UNIAUTH_MYMASK_URL），缺失时返回包含每个缺失变量名的聚合错误列表
    - _Requirements: 9.1, 9.3_
  - [ ]* 2.2 为配置校验编写属性测试（`internal/config/config_test.go`）
    - **Property 18: 环境变量缺失启动失败**
    - **Validates: Requirements 9.3**
    - 使用 gopter，对必填变量的任意非空缺失子集，`Validate()` 返回包含各缺失变量名的错误

- [x] 3. 实现数据库连接与 Schema 迁移
  - [x] 3.1 创建 `internal/database/database.go`：参考 internal 项目，使用 `pgx/v5/pgxpool` 实现 `Connect(ctx, cfg)` 与 `HealthCheck(ctx, pinger)`
    - _Requirements: 9.1_
  - [x] 3.2 创建迁移脚本与执行器：在 `internal/database/migrations/` 用 `go:embed` 内嵌 SQL，`CREATE TABLE IF NOT EXISTS` 创建五张表（`users`、`datasources`、`tickets`、`dms_plans`、`audit_logs`）及索引，字段与设计文档 Data Models 完全一致；实现 `Migrate(ctx, pool)` 幂等执行
    - _Requirements: 9.1_

- [x] 4. 实现 Domain 模型与工单状态机
  - [x] 4.1 创建 `internal/domain/models.go`：定义全部核心类型（`TicketStatus`、`MigrationType`、`TableSelection`、`Ticket`、`Datasource`、`DMSPlan`、`PreconditionWarning`、`AuditLog`、`User`），字段与数据库一一对应（`db` tag），JSONB 字段使用自定义扫描器
    - _Requirements: 4.1, 5.1, 6.1, 8.1_
  - [x] 4.2 创建 `internal/domain/state_machine.go`：实现纯函数 `ValidateTransition(current TicketStatus, action string) error`，合法转换：draft→pending（submit）、pending→approved（approve）、pending→rejected（reject）、approved→plan_ready（generate_plan）、plan_ready→executing（execute）、rejected→pending（resubmit）、executing→completed/failed（status_change）；非法转换返回 `ErrInvalidStateTransition`
    - _Requirements: 4.6, 5.5, 7.2_
  - [ ]* 4.3 为状态机编写属性测试（`internal/domain/state_machine_test.go`）
    - **Property 9: 状态机非法转换拦截**
    - **Validates: Requirements 5.5, 5.4**
    - 使用 gopter，对所有（状态，动作）组合验证合法转换放行、非法转换返回错误

- [x] 5. 实现密码 AES-GCM 加密模块
  - [x] 5.1 创建 `internal/crypto/crypto.go`：实现 `Encrypt(key []byte, plaintext string) (string, error)` 和 `Decrypt(key []byte, ciphertext string) (string, error)`，使用 AES-GCM 随机 nonce，base64 编码存储
    - _Requirements: 3.2_
  - [ ]* 5.2 为加密模块编写单元测试：验证 Encrypt→Decrypt 往返一致，相同明文每次产生不同密文
    - _Requirements: 3.2_

- [x] 6. 实现 UniAuth JWT 认证与 Gin 鉴权中间件
  - [x] 6.1 创建 `internal/auth/jwks.go` 与 `internal/auth/authenticator.go`：参考 internal 项目，实现 JWKS 公钥拉取与本地缓存（带 JWKSTimeout），实现 `Authenticator` 接口，从 `Authorization: Bearer <token>` 验签并提取 `uid`
    - _Requirements: 1.2, 1.3_
  - [x] 6.2 创建 `internal/auth/permission.go`：实现 `PermissionResolver` 接口（调用 `UNIAUTH_MYMASK_URL` 查询权限掩码，带 30s TTL 缓存），定义权限常量 `PermAccess = "dms:access"`、`PermApply = "dms:apply"`、`PermApprove = "dms:approve"`，实现 `HasPermission(mask *big.Int, bitIndex int) bool` 纯函数
    - _Requirements: 2.1, 2.5_
  - [x] 6.3 创建 `internal/auth/middleware.go`（Gin 版）：实现 `AuthMiddleware(authenticator)` —— 验签失败返回 401，JWKS 不可用返回 503；实现 `RequirePermission(permCode string)` —— 无权限返回 403；将 `uid`、`dataScope`（SELF/ALL）注入 `gin.Context`
    - _Requirements: 2.5, 2.6_
  - [x] 6.4 实现用户首次登录自动创建（`internal/repository/user_repo.go`）：`UpsertByUniauthUID(ctx, uid, username, email)` —— 不存在则 INSERT，已存在则复用，同一 uid 不创建多条记录（Property 1）
    - _Requirements: 1.3_
  - [ ]* 6.5 为权限隔离编写属性测试（`internal/auth/permission_test.go`）
    - **Property 2: 角色权限隔离**
    - **Validates: Requirements 2.1, 2.2, 2.3, 2.5, 2.6**
    - 使用 gopter + httptest，对任意（用户角色，API 端点）组合，验证请求结果与权限矩阵一致

- [x] 7. 实现数据源 Repository 与服务层
  - [x] 7.1 创建 `internal/repository/datasource_repo.go`：实现 `DatasourceRepository` 接口（`Create`、`GetByID`、`List`、`Update`、`Delete`、`HasActiveTickets(id int64) bool`），写入前调用 `crypto.Encrypt` 加密密码，所有查询结果中 `password_enc` 置空
    - _Requirements: 3.1, 3.2, 3.5_
  - [x] 7.2 创建 `internal/datasource/service.go`：实现 `CreateDatasource`（加密密码）、`UpdateDatasource`、`DeleteDatasource`（有未终止工单引用时返回 `ErrDatasourceInUse`，Property 5）、`TestConnectivity`（TCP 拨号）；API 响应结构体中 `password` 字段始终置空（Property 3）
    - _Requirements: 3.1, 3.2, 3.4, 3.5_
  - [ ]* 7.3 为密码零泄露编写属性测试
    - **Property 3: 密码零泄露**
    - **Validates: Requirements 3.2, 3.3**
    - 使用 gopter，对任意数据源，List/GetByID 响应体不含明文密码字段
  - [ ]* 7.4 为数据源 round-trip 编写属性测试
    - **Property 4: 数据源创建读取一致性**
    - **Validates: Requirements 3.1**
    - 对任意合法数据源输入，Create 后 GetByID，所有非密码字段值与创建时完全一致
  - [ ]* 7.5 为被引用数据源保护编写属性测试
    - **Property 5: 被引用数据源不可删除**
    - **Validates: Requirements 3.5**
    - 存在未终止工单引用时 DeleteDatasource 返回 `ErrDatasourceInUse`

- [x] 8. 实现 table-mappings 生成与工单 Repository
  - [x] 8.1 创建 `internal/dms/table_mappings.go`：实现纯函数 `BuildTableMappings(selections []TableSelection) string`，每条 `TableSelection` 对应恰好一条 `rule-type = "selection"` inclusion 规则，支持 `%` 通配符（Property 7）
    - _Requirements: 4.2_
  - [x] 8.2 创建 `internal/repository/ticket_repo.go`：实现 `TicketRepository` 接口（`Create`、`GetByID`、`List`（self/all 过滤）、`Update`（仅 draft/rejected）、`UpdateStatus`、`UpdateDMSTask`），`table_selections` JSONB 序列化/反序列化
    - _Requirements: 4.1, 4.5, 5.1_
  - [ ]* 8.3 为 table-mappings 生成编写属性测试（`internal/dms/table_mappings_test.go`）
    - **Property 7: table-mappings 生成正确性**
    - **Validates: Requirements 4.2**
    - 使用 gopter，对任意非空 `[]TableSelection`，rules 数量 = 输入长度，每条 schema/table 与输入一致，无额外规则
  - [ ]* 8.4 为工单必填项校验编写属性测试
    - **Property 6: 工单必填项校验**
    - **Validates: Requirements 4.1, 4.5**
    - 对缺少任意必填字段的工单提交请求，服务层返回验证错误，不创建或变更工单状态

- [x] 9. 实现工单服务层（创建、提交、审核）
  - [x] 9.1 创建 `internal/ticket/service.go` — 工单 CRUD 与提交：`CreateTicket`（draft）、`GetTicket`（self/all）、`ListTickets`（范围过滤）、`UpdateTicket`（仅 draft/rejected，Property 8）、`SubmitTicket`（draft→pending，校验必填项，写 audit_logs）
    - _Requirements: 4.1, 4.4, 4.5, 4.6_
  - [x] 9.2 在 `internal/ticket/service.go` 补充审核操作：`ApproveTicket`（pending→approved，写 reviewer_id/reviewed_at/comment，写 audit_logs）、`RejectTicket`（pending→rejected，comment 必填，写 audit_logs）、`ResubmitTicket`（rejected→pending，写 audit_logs）；所有状态流转调用 `domain.ValidateTransition`（Property 9）
    - _Requirements: 5.2, 5.3, 5.4, 5.5_
  - [ ]* 9.3 为非草稿/驳回工单不可修改编写属性测试
    - **Property 8: 非草稿/驳回工单不可修改**
    - **Validates: Requirements 4.6**
    - 处于 pending/approved/plan_ready/executing/completed/failed 状态的工单，UpdateTicket 返回错误

- [x] 10. 检查点 — 核心层自测
  - 确保 config、database、domain、crypto、auth、datasource、table_mappings 模块的单元测试与属性测试均通过，提问如有疑问。

- [x] 11. 实现 DMS Orchestrator（方案生成与执行）
  - [x] 11.1 创建 `internal/dms/client.go`：定义 `DMSClient` 接口（`CreateReplicationTask`、`StartReplicationTask`、`DescribeReplicationTasks`），实现 `NewAWSDMSClient(cfg)` 生产客户端，实现 `MockDMSClient`（记录调用顺序与入参，返回可配置响应）
    - _Requirements: 7.1_
  - [x] 11.2 实现 `GeneratePlan`（`internal/dms/orchestrator.go`）：从 `srcDS.EndpointARN`/`dstDS.EndpointARN` 读取 ARN，调用 `BuildTableMappings`，引用 `DMS_REPLICATION_INSTANCE_ARN`（Property 10），构建 `precondition_warnings`（endpoint ARN 缺失 → level=error，Property 11），将 `DMSPlan` 写入 `dms_plans`，工单状态流转为 `plan_ready`，写 audit_logs
    - _Requirements: 6.1, 6.2, 6.3, 6.4_
  - [ ]* 11.3 为方案复制实例引用编写属性测试
    - **Property 10: 方案引用正确的复制实例**
    - **Validates: Requirements 6.2**
    - 任何 GeneratePlan 生成的方案，`replication_instance_arn` 等于 `DMS_REPLICATION_INSTANCE_ARN` 环境变量
  - [ ]* 11.4 为前置条件阻止执行编写属性测试
    - **Property 11: 前置条件缺失时阻止执行**
    - **Validates: Requirements 6.4**
    - `precondition_warnings` 含 `level=error` 时，Execute 返回错误
  - [x] 11.5 实现 `Execute`（`internal/dms/orchestrator.go`）：检查前置条件无 error 警告，幂等检查（`dms_task_arn` 已存在跳过 CreateReplicationTask，Property 14），依序调用 `CreateReplicationTask` → `StartReplicationTask` 并回填 ARN（Property 13），工单状态置 `executing`，写 audit_logs，启动后台 goroutine 每 30s 轮询 `DescribeReplicationTasks` 更新 `dms_task_status`（Property 16）
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5_
  - [x] 11.6 实现执行失败处理：AWS API 失败时工单状态→`failed`，`error_detail` 写入错误信息（Property 15），写 audit_logs（action: execute_failed）
    - _Requirements: 7.6_
  - [ ]* 11.7 为执行 API 调用顺序与参数编写属性测试
    - **Property 12: 执行 API 调用顺序与参数正确性**
    - **Validates: Requirements 7.1**
    - 使用 MockDMSClient，验证 CreateReplicationTask 参数来自方案，StartReplicationTask 参数为上步返回的 ARN
  - [ ]* 11.8 为 ARN 回填编写属性测试
    - **Property 13: 执行结果 ARN 回填**
    - **Validates: Requirements 7.3**
    - mock 返回的 `ReplicationTaskArn`，执行后工单的 `dms_task_arn` 等于该值
  - [ ]* 11.9 为执行幂等性编写属性测试
    - **Property 14: 执行幂等性（重试不重复创建）**
    - **Validates: Requirements 7.7**
    - 已有 `dms_task_arn` 的工单再次调用 Execute，MockDMSClient 验证 `CreateReplicationTask` 未被调用
  - [ ]* 11.10 为执行失败保留错误详情编写属性测试
    - **Property 15: 执行失败保留错误详情**
    - **Validates: Requirements 7.6**
    - 任何 AWS API 错误导致工单状态变为 failed，`error_detail` 字段非空
  - [ ]* 11.11 为任务状态轮询一致性编写属性测试
    - **Property 16: 任务状态轮询一致性**
    - **Validates: Requirements 7.4**
    - mock 返回的 DMS 任务状态值，写入 `tickets.dms_task_status` 与返回值完全一致

- [x] 12. 实现审计日志服务
  - [x] 12.1 创建 `internal/audit/repository.go` 与 `internal/audit/service.go`：参考 internal 项目，实现 `AuditService`（`Record(ctx, entry)` 仅增、`QueryByTicket(ctx, ticketID)`），`Record` 写入前验证 `detail` JSONB 不含 `password` 键（Property 17）
    - _Requirements: 8.1, 8.2, 8.3_
  - [ ]* 12.2 为审计日志完整性编写属性测试（`internal/audit/service_test.go`）
    - **Property 17: 审计日志完整性与无密码**
    - **Validates: Requirements 8.1, 8.2, 8.3**
    - 对任意关键操作（submit/approve/reject/generate_plan/execute），audit_logs 存在对应记录含正确 ticket_id/operator_id/action，且 detail 不含 password 键

- [x] 13. 实现 Gin 路由与全部 HTTP 处理层
  - [x] 13.1 创建 `internal/api/router.go` 与 `internal/api/response.go`：参考 internal 项目，实现统一 JSON 响应结构 `{code, message, data}` 与错误映射（401/403/404/409/422/500/503），注册 `GET /healthz`（无需鉴权）
    - _Requirements: 2.5_
  - [x] 13.2 实现认证端点（`internal/api/auth_handler.go`）：`GET /api/dms/auth/me` — 触发用户首次自动创建（Property 1），返回 uid 与 dms 权限位
    - _Requirements: 1.3_
  - [x] 13.3 实现数据源 Handlers（`internal/api/datasource_handler.go`）：`GET /api/dms/datasources`（dms:access）、`POST`（dms:approve）、`PUT /api/dms/datasources/:id`（dms:approve）、`DELETE /api/dms/datasources/:id`（dms:approve）、`POST /api/dms/datasources/:id/test`（dms:approve）；所有响应体中 password 不输出（Property 3）
    - _Requirements: 3.1, 3.2, 3.4, 3.5_
  - [x] 13.4 实现工单 Handlers（`internal/api/ticket_handler.go`）：`GET /api/dms/tickets`（dms:access，self/all 范围）、`POST`（dms:apply）、`GET /api/dms/tickets/:id`（dms:access）、`PUT /api/dms/tickets/:id`（dms:apply）、`POST /:id/submit`（dms:apply）、`POST /:id/approve`（dms:approve）、`POST /:id/reject`（dms:approve）
    - _Requirements: 4.1, 4.5, 4.6, 5.2, 5.3, 5.5_
  - [x] 13.5 实现方案与执行 Handlers（`internal/api/plan_handler.go`）：`POST /api/dms/tickets/:id/generate-plan`（dms:approve）、`GET /:id/plan`（dms:approve）、`POST /:id/execute`（dms:approve）、`GET /:id/audit`（dms:access）
    - _Requirements: 6.1, 6.3, 6.4, 7.1, 8.2_
  - [ ]* 13.6 为认证中间件编写集成测试（`internal/api/router_test.go`）：使用 httptest + mock Authenticator，验证无 token→401、token 无效→401、JWKS 不可用→503
    - _Requirements: 1.3, 2.5_
  - [ ]* 13.7 为首次登录自动创建编写属性测试
    - **Property 1: UniAuth 首次登录自动创建账号（幂等）**
    - **Validates: Requirements 1.3**
    - 同一 uid 重复调用 auth/me，`users` 表只有一条记录，`uniauth_uid = token.uid` 恒成立

- [x] 14. 检查点 — 完整工单流程集成测试
  - 使用 testcontainers-go（PostgreSQL）+ MockDMSClient，验证完整流程：创建草稿 → 提交 → 审核 → 生成方案 → 执行
  - 确保所有集成测试通过，提问如有疑问。

- [x] 15. 完善入口程序与 K8s 部署清单
  - [x] 15.1 完善 `cmd/server/main.go`：`config.Load()` → `database.Connect()` → `database.Migrate()` → 初始化所有服务与依赖注入 → `api.NewRouter()` → Gin server 启动，监听 SIGTERM 优雅关闭
    - _Requirements: 9.1, 9.3_
  - [x] 15.2 创建 `deploy/dms-access.yaml`：`Deployment`（镜像占位、环境变量全部来自 Secret/ConfigMap）、`Service`（ClusterIP）、Ingress（路径 `/api/dms/` 转发后端），`deploy/rbac.yaml`（ServiceAccount + IRSA 注解），不硬编码任何密钥值
    - _Requirements: 9.2, 9.4_

- [x] 16. 最终检查点 — 全量测试与编译验证
  - `go build ./...` 确认全量编译无错误
  - `go test ./...` 确认所有单元测试、属性测试通过，提问如有疑问。

## Notes

- 标注 `*` 的子任务为可选测试任务，可跳过以加快 MVP 交付，建议合并主干前补全
- 所有代码结构、认证模式、错误响应均参考 `/Users/5miles/github/internal` 项目对应模块
- **本任务列表仅含后端 Go 实现**，不含前端页面、TS 类型定义等任何前端工作
- 密码在任何 API 响应中均不输出（Property 3）；`ENCRYPTION_KEY` 环境变量注入 AES-GCM 密钥
- 所有状态流转通过 `domain.ValidateTransition` 纯函数统一校验（Property 9）
- AWS DMS 调用通过 `DMSClient` 接口注入，测试使用 `MockDMSClient`（Properties 12–14）
- 属性测试使用 `gopter`，每条属性至少运行 100 次随机输入；注释格式：`// Feature: mysql-redshift-migration-approval, Property N: <property_text>`
- DMS Endpoint ARN 由运维预先在 AWS Console 创建后录入 datasource，系统在 `generate-plan` 时引用

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["2.1", "3.1", "4.1", "5.1"] },
    { "id": 1, "tasks": ["2.2", "3.2", "4.2", "5.2", "6.1"] },
    { "id": 2, "tasks": ["4.3", "6.2", "6.3", "6.4", "8.1"] },
    { "id": 3, "tasks": ["6.5", "7.1", "8.2", "8.3"] },
    { "id": 4, "tasks": ["7.2", "7.3", "7.4", "7.5", "8.4", "9.1"] },
    { "id": 5, "tasks": ["9.2", "9.3", "11.1", "12.1"] },
    { "id": 6, "tasks": ["11.2", "11.6", "12.2"] },
    { "id": 7, "tasks": ["11.3", "11.4", "11.5"] },
    { "id": 8, "tasks": ["11.7", "11.8", "11.9", "11.10", "11.11", "13.1"] },
    { "id": 9, "tasks": ["13.2", "13.3"] },
    { "id": 10, "tasks": ["13.4", "13.5"] },
    { "id": 11, "tasks": ["13.6", "13.7", "15.1"] },
    { "id": 12, "tasks": ["15.2"] }
  ]
}
```
