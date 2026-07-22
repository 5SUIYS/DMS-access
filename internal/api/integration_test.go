// Package api 集成测试 —— 使用 httptest + 内存 mock（无需 PostgreSQL）
// 验证完整工单流程：创建 → 提交 → 审核通过 → 生成方案 → 执行
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5miles/dms-access/internal/audit"
	"github.com/5miles/dms-access/internal/auth"
	"github.com/5miles/dms-access/internal/datasource"
	dmssvc "github.com/5miles/dms-access/internal/dms"
	"github.com/5miles/dms-access/internal/domain"
	"github.com/5miles/dms-access/internal/repository"
	"github.com/5miles/dms-access/internal/ticket"
)

// ─────────────────────────────────────────────
// 内存 Mock：TicketRepository
// ─────────────────────────────────────────────

type memTicketRepo struct {
	mu      sync.Mutex
	tickets map[int64]*domain.Ticket
	nextID  int64
}

func newMemTicketRepo() *memTicketRepo {
	return &memTicketRepo{tickets: make(map[int64]*domain.Ticket), nextID: 1}
}

func (r *memTicketRepo) Create(_ context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *t
	cp.ID = r.nextID
	r.nextID++
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = time.Now()
	r.tickets[cp.ID] = &cp
	result := cp
	return &result, nil
}

func (r *memTicketRepo) GetByID(_ context.Context, id int64) (*domain.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[id]
	if !ok {
		return nil, repository.ErrTicketNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *memTicketRepo) List(_ context.Context, filter repository.TicketFilter) ([]*domain.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Ticket
	for _, t := range r.tickets {
		if filter.Status != "" && string(t.Status) != filter.Status {
			continue
		}
		cp := *t
		result = append(result, &cp)
	}
	return result, nil
}

func (r *memTicketRepo) Update(_ context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.tickets[t.ID]
	if !ok {
		return nil, repository.ErrTicketNotFound
	}
	if !domain.CanModify(cur.Status) {
		return nil, repository.ErrTicketNotModifiable
	}
	cp := *t
	cp.UpdatedAt = time.Now()
	r.tickets[t.ID] = &cp
	result := cp
	return &result, nil
}

func (r *memTicketRepo) UpdateStatus(_ context.Context, id int64, status domain.TicketStatus, fields map[string]interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[id]
	if !ok {
		return repository.ErrTicketNotFound
	}
	t.Status = status
	t.UpdatedAt = time.Now()
	for k, v := range fields {
		switch k {
		case "submitter_id":
			if vid, ok := v.(int64); ok {
				t.SubmitterID = &vid
			}
		case "submitted_at":
			if vt, ok := v.(time.Time); ok {
				t.SubmittedAt = &vt
			}
		case "reviewer_id":
			if vid, ok := v.(int64); ok {
				t.ReviewerID = &vid
			}
		case "reviewed_at":
			if vt, ok := v.(time.Time); ok {
				t.ReviewedAt = &vt
			}
		case "review_comment":
			if vs, ok := v.(string); ok {
				t.ReviewComment = vs
			}
		case "executor_id":
			if vid, ok := v.(int64); ok {
				t.ExecutorID = &vid
			}
		case "executed_at":
			if vt, ok := v.(time.Time); ok {
				t.ExecutedAt = &vt
			}
		case "error_detail":
			if vs, ok := v.(string); ok {
				t.ErrorDetail = vs
			}
		}
	}
	return nil
}

func (r *memTicketRepo) UpdateDMSTask(_ context.Context, id int64, taskARN, taskStatus string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tickets[id]
	if !ok {
		return repository.ErrTicketNotFound
	}
	t.DMSTaskARN = taskARN
	t.DMSTaskStatus = taskStatus
	return nil
}

// ─────────────────────────────────────────────
// 内存 Mock：PlanRepository
// ─────────────────────────────────────────────

type memPlanRepo struct {
	mu     sync.Mutex
	plans  map[int64]*domain.DMSPlan // keyed by ticketID
	nextID int64
}

func newMemPlanRepo() *memPlanRepo {
	return &memPlanRepo{plans: make(map[int64]*domain.DMSPlan), nextID: 1}
}

func (r *memPlanRepo) Create(_ context.Context, plan *domain.DMSPlan) (*domain.DMSPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *plan
	cp.ID = r.nextID
	r.nextID++
	cp.GeneratedAt = time.Now()
	r.plans[cp.TicketID] = &cp
	result := cp
	return &result, nil
}

func (r *memPlanRepo) GetByTicketID(_ context.Context, ticketID int64) (*domain.DMSPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plans[ticketID]
	if !ok {
		return nil, repository.ErrPlanNotFound
	}
	cp := *p
	return &cp, nil
}

// ─────────────────────────────────────────────
// 内存 Mock：AuditRepository
// ─────────────────────────────────────────────

type memAuditRepo struct {
	mu   sync.Mutex
	logs []*domain.AuditLog
}

func (r *memAuditRepo) Create(_ context.Context, log *domain.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *log
	r.logs = append(r.logs, &cp)
	return nil
}

func (r *memAuditRepo) ListByTicket(_ context.Context, ticketID int64) ([]*domain.AuditLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.AuditLog
	for _, l := range r.logs {
		if l.TicketID != nil && *l.TicketID == ticketID {
			cp := *l
			result = append(result, &cp)
		}
	}
	return result, nil
}

// ─────────────────────────────────────────────
// 内存 Mock：DatasourceRepository
// ─────────────────────────────────────────────

type memDatasourceRepo struct {
	mu          sync.Mutex
	datasources map[int64]*domain.Datasource
	nextID      int64
}

func newMemDatasourceRepo() *memDatasourceRepo {
	return &memDatasourceRepo{datasources: make(map[int64]*domain.Datasource), nextID: 1}
}

func (r *memDatasourceRepo) Create(_ context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *ds
	cp.ID = r.nextID
	r.nextID++
	r.datasources[cp.ID] = &cp
	result := cp
	return &result, nil
}

func (r *memDatasourceRepo) GetByID(_ context.Context, id int64) (*domain.Datasource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds, ok := r.datasources[id]
	if !ok {
		return nil, repository.ErrDatasourceNotFound
	}
	cp := *ds
	return &cp, nil
}

func (r *memDatasourceRepo) List(_ context.Context) ([]*domain.Datasource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Datasource
	for _, ds := range r.datasources {
		cp := *ds
		result = append(result, &cp)
	}
	return result, nil
}

func (r *memDatasourceRepo) Update(_ context.Context, ds *domain.Datasource) (*domain.Datasource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.datasources[ds.ID]; !ok {
		return nil, repository.ErrDatasourceNotFound
	}
	cp := *ds
	r.datasources[ds.ID] = &cp
	result := cp
	return &result, nil
}

func (r *memDatasourceRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.datasources[id]; !ok {
		return repository.ErrDatasourceNotFound
	}
	delete(r.datasources, id)
	return nil
}

func (r *memDatasourceRepo) HasActiveTickets(_ context.Context, _ int64) (bool, error) {
	return false, nil
}

// ─────────────────────────────────────────────
// Mock：Authenticator（可控制验签成功/失败）
// ─────────────────────────────────────────────

type mockAuthenticator struct {
	uid string
	err error
}

func (m *mockAuthenticator) Authenticate(_ context.Context, _ string) (string, error) {
	return m.uid, m.err
}

func (m *mockAuthenticator) AuthenticateIdentity(_ context.Context, _ string) (auth.Identity, error) {
	if m.err != nil {
		return auth.Identity{}, m.err
	}
	return auth.Identity{UID: m.uid, Username: m.uid}, nil
}

// ─────────────────────────────────────────────
// Mock：PermissionResolver（按 uid 返回权限位）
// ─────────────────────────────────────────────

type mockPermResolver struct {
	// uid → bit index set（拥有的权限位）
	perms map[string][]int
	// permCode → bit index 字典
	dict map[string]int
}

func newMockPermResolver() *mockPermResolver {
	return &mockPermResolver{
		perms: make(map[string][]int),
		dict: map[string]int{
			auth.PermAccess:  0,
			auth.PermApply:   1,
			auth.PermApprove: 2,
		},
	}
}

// grant 为指定 uid 授权
func (r *mockPermResolver) grant(uid string, perms ...string) {
	for _, p := range perms {
		if idx, ok := r.dict[p]; ok {
			r.perms[uid] = append(r.perms[uid], idx)
		}
	}
}

func (r *mockPermResolver) Mask(_ context.Context, uid string) (*big.Int, string, error) {
	mask := new(big.Int)
	for _, idx := range r.perms[uid] {
		mask.SetBit(mask, idx, 1)
	}
	// 有 approve 权限 → ALL 范围
	scope := "SELF"
	if mask.Bit(r.dict[auth.PermApprove]) == 1 {
		scope = "ALL"
	}
	return mask, scope, nil
}

func (r *mockPermResolver) BitOf(_ context.Context, permCode string) (int, error) {
	idx, ok := r.dict[permCode]
	if !ok {
		return 0, auth.ErrUnknownPermission
	}
	return idx, nil
}

// ─────────────────────────────────────────────
// 测试辅助：构建测试路由
// ─────────────────────────────────────────────

// testEnv 封装一次集成测试的全部组件。
type testEnv struct {
	router      http.Handler
	ticketRepo  *memTicketRepo
	planRepo    *memPlanRepo
	auditRepo   *memAuditRepo
	dsRepo      *memDatasourceRepo
	resolver    *mockPermResolver
	authn       *mockAuthenticator
	// uid → 内部数据库 userID（从 1 开始）
	userIDs map[string]int64
}

func newTestEnv() *testEnv {
	ticketRepo := newMemTicketRepo()
	planRepo := newMemPlanRepo()
	auditRepo := &memAuditRepo{}
	dsRepo := newMemDatasourceRepo()
	resolver := newMockPermResolver()

	authn := &mockAuthenticator{uid: "dev-uid"}

	auditSvc := audit.NewService(auditRepo)
	ticketSvc := ticket.NewService(ticketRepo, auditRepo)

	dsSvc := datasource.NewService(dsRepo)

	mockDMS := &dmssvc.MockDMSClient{}
	orch := dmssvc.NewOrchestrator(
		mockDMS,
		ticketRepo,
		planRepo,
		dsRepo,
		auditRepo,
		"arn:aws:dms:us-east-1:123456789:rep:test-instance",
	)

	userIDs := map[string]int64{
		"dev-uid": 1,
		"ops-uid": 2,
	}

	deps := Deps{
		Authenticator: authn,
		Resolver:      resolver,
		DatasourceSvc: dsSvc,
		TicketSvc:     ticketSvc,
		Orchestrator:  orch,
		PlanRepo:      planRepo,
		AuditSvc:      auditSvc,
		UserIDFunc: func(_ context.Context, uid string) int64 {
			if id, ok := userIDs[uid]; ok {
				return id
			}
			return 0
		},
	}

	return &testEnv{
		router:     NewRouter(deps),
		ticketRepo: ticketRepo,
		planRepo:   planRepo,
		auditRepo:  auditRepo,
		dsRepo:     dsRepo,
		resolver:   resolver,
		authn:      authn,
		userIDs:    userIDs,
	}
}

// do 执行 HTTP 请求并返回 ResponseRecorder。
func (e *testEnv) do(method, path string, body interface{}, bearerUID string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerUID != "" {
		req.Header.Set("Authorization", "Bearer "+bearerUID)
	}
	// 让 mockAuthenticator 根据 bearer token 值决定 uid
	e.authn.uid = bearerUID
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// seedDatasources 预置两个数据源（src + dst），返回它们的 ID。
func (e *testEnv) seedDatasources(t *testing.T) (srcID, dstID int64) {
	t.Helper()
	src := &domain.Datasource{
		Name:        "src-mysql",
		Type:        "mysql",
		EndpointARN: "arn:aws:dms:us-east-1:123:endpoint:src",
	}
	dst := &domain.Datasource{
		Name:        "dst-redshift",
		Type:        "redshift",
		EndpointARN: "arn:aws:dms:us-east-1:123:endpoint:dst",
	}
	s, _ := e.dsRepo.Create(context.Background(), src)
	d, _ := e.dsRepo.Create(context.Background(), dst)
	return s.ID, d.ID
}

// ─────────────────────────────────────────────
// 测试 1：认证中间件 —— 无 token 返回 401
// ─────────────────────────────────────────────

func TestAuth_NoToken_Returns401(t *testing.T) {
	env := newTestEnv()
	// 配置 authenticator：无 token 时返回 ErrNoToken
	env.authn.uid = ""
	env.authn.err = auth.ErrNoToken

	w := env.do("GET", "/api/dms/tickets", nil, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "UNAUTHORIZED", resp["code"])
}

// ─────────────────────────────────────────────
// 测试 2：权限中间件 —— 有 token 但无权限返回 403
// ─────────────────────────────────────────────

func TestPermission_NoPermission_Returns403(t *testing.T) {
	env := newTestEnv()
	// dev-uid 只有 access 权限，调用需要 approve 的端点
	env.resolver.grant("dev-uid", auth.PermAccess)
	// authn 成功验证
	env.authn.err = nil

	// POST /api/dms/tickets 需要 dms:apply，dev-uid 没有
	w := env.do("POST", "/api/dms/tickets", map[string]interface{}{
		"src_datasource_id": 1,
		"dst_datasource_id": 2,
		"target_schema":     "mydb",
		"migration_type":    "full-load",
		"table_selections":  []map[string]string{{"schema_name": "mydb", "table_name": "orders"}},
		"reason":            "test",
	}, "dev-uid")
	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "FORBIDDEN", resp["code"])
}

// ─────────────────────────────────────────────
// 测试 3：完整工单状态流转
// ─────────────────────────────────────────────

func TestFullTicketWorkflow(t *testing.T) {
	env := newTestEnv()

	// dev-uid 拥有 access + apply 权限
	env.resolver.grant("dev-uid", auth.PermAccess, auth.PermApply)
	// ops-uid 拥有全部权限
	env.resolver.grant("ops-uid", auth.PermAccess, auth.PermApply, auth.PermApprove)
	env.authn.err = nil

	srcID, dstID := env.seedDatasources(t)

	// ── 步骤 1：创建工单 POST /api/dms/tickets → 201 ──────────────────────
	createBody := map[string]interface{}{
		"title":           "测试同步工单",
		"src_datasource_id": srcID,
		"dst_datasource_id": dstID,
		"target_schema":   "mydb",
		"migration_type":  "full-load",
		"table_selections": []map[string]string{
			{"schema_name": "mydb", "table_name": "orders"},
		},
		"reason": "集成测试",
	}
	w := env.do("POST", "/api/dms/tickets", createBody, "dev-uid")
	require.Equal(t, http.StatusCreated, w.Code, "创建工单应返回 201，body: %s", w.Body.String())

	var createdTicket domain.Ticket
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createdTicket))
	ticketID := createdTicket.ID
	require.Greater(t, ticketID, int64(0))
	assert.Equal(t, domain.StatusDraft, createdTicket.Status)

	// ── 步骤 2：提交工单 POST /api/dms/tickets/:id/submit → pending ────────
	w = env.do("POST", "/api/dms/tickets/1/submit", nil, "dev-uid")
	require.Equal(t, http.StatusOK, w.Code, "提交工单应返回 200，body: %s", w.Body.String())

	var submittedTicket domain.Ticket
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &submittedTicket))
	assert.Equal(t, domain.StatusPending, submittedTicket.Status)

	// ── 步骤 3：审核通过 POST /api/dms/tickets/:id/approve → approved ──────
	w = env.do("POST", "/api/dms/tickets/1/approve", map[string]string{"comment": "LGTM"}, "ops-uid")
	require.Equal(t, http.StatusOK, w.Code, "审核通过应返回 200，body: %s", w.Body.String())

	var approvedTicket domain.Ticket
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &approvedTicket))
	assert.Equal(t, domain.StatusApproved, approvedTicket.Status)

	// ── 步骤 4：生成方案 POST /api/dms/tickets/:id/generate-plan → plan_ready
	w = env.do("POST", "/api/dms/tickets/1/generate-plan", nil, "ops-uid")
	require.Equal(t, http.StatusOK, w.Code, "生成方案应返回 200，body: %s", w.Body.String())

	var plan domain.DMSPlan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan))
	assert.Equal(t, ticketID, plan.TicketID)

	// 验证工单状态已变为 plan_ready
	t1, err := env.ticketRepo.GetByID(context.Background(), ticketID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPlanReady, t1.Status)

	// ── 步骤 5：执行 POST /api/dms/tickets/:id/execute → executing ──────────
	w = env.do("POST", "/api/dms/tickets/1/execute", nil, "ops-uid")
	require.Equal(t, http.StatusOK, w.Code, "执行应返回 200，body: %s", w.Body.String())

	var executingTicket domain.Ticket
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &executingTicket))
	assert.Equal(t, domain.StatusExecuting, executingTicket.Status)
}

// ─────────────────────────────────────────────
// 测试 4：状态机非法转换 —— 直接 approve 草稿工单应返回 409
// ─────────────────────────────────────────────

func TestInvalidStateTransition_Returns409(t *testing.T) {
	env := newTestEnv()
	env.resolver.grant("dev-uid", auth.PermAccess, auth.PermApply)
	env.resolver.grant("ops-uid", auth.PermAccess, auth.PermApply, auth.PermApprove)
	env.authn.err = nil

	srcID, dstID := env.seedDatasources(t)

	// 创建工单（draft 状态）
	createBody := map[string]interface{}{
		"src_datasource_id": srcID,
		"dst_datasource_id": dstID,
		"target_schema":     "mydb",
		"migration_type":    "full-load",
		"table_selections":  []map[string]string{{"schema_name": "mydb", "table_name": "t"}},
		"reason":            "test",
	}
	w := env.do("POST", "/api/dms/tickets", createBody, "dev-uid")
	require.Equal(t, http.StatusCreated, w.Code)

	// 直接 approve（跳过 submit），应返回 409
	w = env.do("POST", "/api/dms/tickets/1/approve", map[string]string{"comment": "skip"}, "ops-uid")
	assert.Equal(t, http.StatusConflict, w.Code, "直接 approve 草稿工单应返回 409，body: %s", w.Body.String())
}

// ─────────────────────────────────────────────
// 测试 5：健康检查端点无需认证
// ─────────────────────────────────────────────

func TestHealthz_NoAuth(t *testing.T) {
	env := newTestEnv()
	// authn 设置为必定失败，但 /healthz 不经过认证中间件
	env.authn.err = auth.ErrNoToken

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	// 只要不是 401/403 即可（实现可能返回 200 或 503 取决于 healthFunc）
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}
