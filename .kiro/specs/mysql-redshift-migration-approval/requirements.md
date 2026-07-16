# Requirements Document

> MySQL → Redshift 同步审批系统（DMS-access）

## Introduction

DMS-access 是一个工单审批 + AWS 编排系统，用于管理"将 MySQL 数据同步到 Amazon Redshift"这一类需求。开发人员发起同步申请，运维人员审核；审核通过后，系统生成完整的 AWS DMS 执行方案，运维确认后由系统调用 AWS DMS API 自动创建端点、复制任务并启动同步（半自动模式：人工确认为最后一道闸门）。

**v1 范围边界：**
- 业务类型只有一种：MySQL → Redshift 同步。
- 执行模式为半自动（系统生成方案 + 运维确认后系统执行）。
- 复用一个已存在的常驻 DMS 复制实例（不动态创建 Serverless 复制）。
- 认证走 UniAuth SSO，同时保留本地账号登录。
- 部署在 K8s，配置全部通过环境变量注入（为后续迁移 EC2 预留）。

**技术栈：** 前端 Next.js + TypeScript，后端 Go（Gin），数据库 PostgreSQL，AWS 集成用 aws-sdk-go-v2。

---

## Glossary

| 术语 | 含义 |
|------|------|
| 申请人/开发 | 发起同步申请的用户 |
| 审核人/运维 | 审核并执行同步的用户 |
| 数据源（Datasource） | 预先登记的 MySQL 源库或 Redshift 目标库的连接信息 |
| 工单（Migration Request） | 一次 MySQL→Redshift 同步申请 |
| DMS 执行方案（DMS Plan） | 系统根据工单生成的 AWS DMS 端点/任务/表映射配置 |
| 复制实例 | 常驻的 AWS DMS Replication Instance，运行迁移任务 |
| 表映射（Table Mapping） | 指定同步哪些库/表的 JSON 规则 |
| 迁移类型 | full-load / cdc / full-load-and-cdc |

---

## Requirements

### 需求 1：用户认证与登录

**用户故事：** 作为系统用户，我希望能通过 UniAuth SSO 或本地账号登录，以便安全地访问系统。

#### 验收标准

1. WHEN 用户访问系统且未登录 THEN 系统 SHALL 展示登录页，提供"UniAuth 登录"和"本地账号登录"两种方式。
2. WHEN 用户点击"UniAuth 登录" THEN 系统 SHALL 跳转到 UniAuth 登录页并携带回调地址。
3. WHEN UniAuth 登录成功回调 THEN 系统 SHALL 校验 token（调用 my-mask 接口），首次登录的用户 SHALL 自动创建本地账号并保存 uniauth uid 映射。
4. WHEN 用户使用本地账号登录且账号密码正确 THEN 系统 SHALL 签发会话 token 并进入首页。
5. IF UniAuth 未启用（环境变量 `UNIAUTH_ENABLE` 非 true） THEN 系统 SHALL 隐藏 UniAuth 登录入口且不影响本地登录。
6. WHEN 用户登出 THEN 系统 SHALL 清除本地会话；若为 SSO 用户 THEN 系统 SHALL 同时跳转 UniAuth 登出。

### 需求 2：权限与角色控制

**用户故事：** 作为系统，我需要区分开发和运维两种角色的权限，以便控制谁能提交、谁能审核和执行。

#### 验收标准

1. 系统 SHALL 通过 UniAuth 权限位掩码（app_code=`dms-access`）判定用户功能权限。
2. WHERE 用户为开发角色 THE 系统 SHALL 允许其创建工单、提交工单、查看自己的工单（Self 数据范围）。
3. WHERE 用户为运维角色 THE 系统 SHALL 允许其审核工单、生成执行方案、确认执行、查看全部工单（All 数据范围）。
4. WHEN 用户访问无权限的功能 THEN 前端 SHALL 将对应按钮置灰并提示无权限（而非隐藏）。
5. WHEN 后端收到请求 THEN 系统 SHALL 在中间件层校验 token 与权限位，无权限时返回 403。
6. WHEN 开发尝试审核或执行工单 THEN 系统 SHALL 拒绝并返回 403。

### 需求 3：数据源登记与管理

**用户故事：** 作为运维，我希望预先登记 MySQL 源和 Redshift 目标的连接信息，以便开发提交工单时只需选择而无需填写敏感连接信息。

#### 验收标准

1. WHEN 运维创建数据源 THEN 系统 SHALL 记录类型（mysql/redshift）、名称、环境（test/prod）、host、port、用户名、密码、region 及扩展配置。
2. WHEN 保存数据源密码 THEN 系统 SHALL 加密存储密码，且任何查询接口 SHALL NOT 返回明文密码。
3. WHEN 开发创建工单 THEN 系统 SHALL 仅提供数据源的名称/环境等非敏感字段供选择。
4. WHEN 运维测试数据源连通性 THEN 系统 SHALL 尝试连接并返回成功或失败原因。
5. IF 数据源已被某个未完成的工单引用 THEN 系统 SHALL 阻止删除该数据源并给出提示。

### 需求 4：创建与提交同步工单

**用户故事：** 作为开发，我希望填写一个同步申请单（选源库、选目标库、选要同步的表、选同步类型），以便向运维发起同步请求。

#### 验收标准

1. WHEN 开发创建工单 THEN 系统 SHALL 要求选择源 MySQL 数据源、目标 Redshift 数据源、目标 schema、迁移类型（full-load/cdc/full-load-and-cdc）、要同步的库/表、申请原因。
2. WHEN 开发选择要同步的表 THEN 系统 SHALL 支持按库/表选择或通配规则，并生成对应的表映射（table mapping）。
3. WHEN 迁移类型包含 CDC（cdc 或 full-load-and-cdc） THEN 系统 SHALL 提示源 MySQL 需已开启 binlog（ROW 格式），并在方案生成阶段校验。
4. WHEN 开发保存但未提交 THEN 系统 SHALL 将工单置为草稿（draft）状态，允许后续编辑。
5. WHEN 开发提交工单 THEN 系统 SHALL 校验必填项完整后将状态置为待审核（pending）并记录申请人与时间。
6. WHEN 工单处于待审核或之后的状态 THEN 系统 SHALL 禁止申请人再修改工单内容。

### 需求 5：工单审核

**用户故事：** 作为运维，我希望查看待审核工单的完整信息并做出通过或驳回决定，以便控制同步操作的安全性。

#### 验收标准

1. WHEN 运维查看待审核工单 THEN 系统 SHALL 展示源/目标数据源、目标 schema、迁移类型、表映射、申请人、申请原因等完整信息。
2. WHEN 运维通过工单 THEN 系统 SHALL 将状态置为已通过（approved），记录审核人、审核意见、审核时间。
3. WHEN 运维驳回工单 THEN 系统 SHALL 将状态置为驳回（rejected）并要求填写驳回原因，退回给申请人。
4. WHEN 工单被驳回 THEN 系统 SHALL 允许申请人查看驳回原因并基于原工单重新编辑提交。
5. WHEN 工单状态非待审核 THEN 系统 SHALL 禁止对其执行审核操作。

### 需求 6：生成 DMS 执行方案

**用户故事：** 作为运维，我希望系统根据审核通过的工单自动生成完整的 AWS DMS 执行方案，以便我在执行前预览确认。

#### 验收标准

1. WHEN 工单状态为已通过 THEN 系统 SHALL 根据工单与登记的数据源自动生成 DMS 执行方案，包含：源端点参数、目标端点参数、复制任务配置、表映射 JSON。
2. WHEN 生成执行方案 THEN 系统 SHALL 引用预先配置的常驻复制实例（不创建新实例）。
3. WHEN 生成执行方案 THEN 系统 SHALL 将方案以可读形式（含 table-mapping JSON 与 task settings）保存并展示给运维预览。
4. IF 生成方案所需的前置资源缺失（如目标 IAM 角色、复制实例不可用） THEN 系统 SHALL 在预览中明确标记缺失项并阻止执行。
5. WHEN 迁移类型包含 CDC 且系统可探测源 binlog 状态 THEN 系统 SHALL 校验 binlog 配置并在方案中给出结论。

### 需求 7：半自动执行同步

**用户故事：** 作为运维，我希望在确认执行方案无误后一键触发系统执行，以便自动完成 AWS DMS 端点与任务的创建和启动。

#### 验收标准

1. WHEN 运维在执行确认页点击"执行" THEN 系统 SHALL 按序调用 AWS DMS API：创建源端点 → 创建目标端点 → 创建复制任务 → 启动复制任务。
2. WHEN 执行开始 THEN 系统 SHALL 将工单状态置为执行中（executing）并记录操作人与时间。
3. WHEN AWS API 返回资源标识 THEN 系统 SHALL 将 endpoint ARN、task ARN 等回填到工单。
4. WHILE 复制任务运行中 THE 系统 SHALL 定期轮询任务状态并更新工单进度。
5. WHEN 复制任务成功 THEN 系统 SHALL 将工单状态置为完成（completed）。
6. IF 执行过程中任一步骤失败 THEN 系统 SHALL 将工单状态置为失败（failed），记录错误详情，并保留已创建资源的标识以便排查。
7. WHEN 执行失败后运维重试 THEN 系统 SHALL 支持在修复问题后重新触发执行，且 SHALL NOT 重复创建已存在的资源。

### 需求 8：操作审计

**用户故事：** 作为运维/管理员，我希望系统记录所有关键操作，以便事后追溯谁在何时对哪个工单做了什么。

#### 验收标准

1. WHEN 发生工单创建、提交、审核、方案生成、执行、状态变更等关键操作 THEN 系统 SHALL 写入审计日志，记录操作人、动作、时间、关联工单与详情。
2. WHEN 运维/管理员查看某工单 THEN 系统 SHALL 提供该工单的完整操作时间线。
3. 系统 SHALL NOT 在审计日志中记录明文密码或完整凭证。

### 需求 9：配置与部署

**用户故事：** 作为运维，我希望系统所有环境相关配置都能通过环境变量注入，以便在 K8s 部署并为迁移 EC2 预留灵活性。

#### 验收标准

1. 系统 SHALL 通过环境变量读取以下配置：数据库连接、UniAuth 相关（ENABLE/URL/APP_CODE 等）、AWS region、DMS 复制实例标识、加密密钥。
2. WHEN 部署到 K8s THEN 系统 SHALL 提供后端与前端的部署清单（Deployment/Service/Ingress 或等价配置）。
3. IF 关键环境变量缺失 THEN 系统 SHALL 在启动时报错并给出明确提示，而非静默使用错误默认值。
4. 系统 SHALL 通过 IAM 角色（IRSA 或等价机制）获取 AWS DMS 调用权限，而非在配置中硬编码长期凭证。
5. 系统 SHALL 提供一次性前置检查/文档，覆盖 `dms-access-for-endpoint`、`dms-vpc-role`、复制实例、源 binlog 等 AWS 前置依赖。
