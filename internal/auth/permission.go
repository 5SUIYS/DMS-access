package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DMS 权限位 code 定义（需求 2.1）。
const (
	// PermAccess 访问 DMS 模块（dms:access）。
	PermAccess = "dms:access"
	// PermApply 提交工单（dms:apply）。
	PermApply = "dms:apply"
	// PermApprove 审核、生成方案、执行（dms:approve）。
	PermApprove = "dms:approve"
)

// 权限解析相关错误。
var (
	// ErrPermissionUnavailable 表示向 UniAuth 拉取权限掩码或字典失败/超时。
	ErrPermissionUnavailable = errors.New("auth: 权限服务不可用")
	// ErrUnknownPermission 表示 permCode 不在 UniAuth 权限字典中。
	ErrUnknownPermission = errors.New("auth: 未知的权限 code")
)

// HasPermission 是判权纯函数（需求 2.5）：当且仅当权限掩码的第 bitIndex 位为 1 时返回 true。
func HasPermission(mask *big.Int, bitIndex int) bool {
	if mask == nil || bitIndex < 0 {
		return false
	}
	return mask.Bit(bitIndex) == 1
}

// PermissionResolver 解析用户权限掩码与权限位字典（需求 2）。
type PermissionResolver interface {
	// Mask 返回 uid 的权限掩码与数据归属范围。
	Mask(ctx context.Context, uid string) (mask *big.Int, dataScope string, err error)
	// BitOf 返回权限 code 对应的 bit 索引。
	BitOf(ctx context.Context, permCode string) (bitIndex int, err error)
}

// DefaultPermissionHTTPTimeout 为拉取权限掩码/字典的默认超时时间。
const DefaultPermissionHTTPTimeout = 5 * time.Second

type maskEntry struct {
	mask      *big.Int
	dataScope string
	expiresAt time.Time
}

type dictEntry struct {
	dict      map[string]int
	expiresAt time.Time
}

type httpPermissionResolver struct {
	myMaskURL string
	appCode   string
	ttl       time.Duration
	timeout   time.Duration
	client    *http.Client
	now       func() time.Time

	maskMu    sync.Mutex
	maskCache map[string]maskEntry

	dictMu    sync.Mutex
	dictCache *dictEntry
}

// NewPermissionResolver 创建一个基于 UniAuth HTTP 接口、带短 TTL 缓存的 PermissionResolver。
// myMaskURL 来自 config.UniAuth.MyMaskURL，ttl 约 30s。
func NewPermissionResolver(myMaskURL, appCode string, ttl time.Duration) PermissionResolver {
	r := &httpPermissionResolver{
		myMaskURL: strings.TrimRight(myMaskURL, "/"),
		appCode:   appCode,
		ttl:       ttl,
		timeout:   DefaultPermissionHTTPTimeout,
		client:    &http.Client{},
		now:       time.Now,
		maskCache: make(map[string]maskEntry),
	}
	if r.ttl <= 0 {
		r.ttl = 30 * time.Second
	}
	return r
}

// Mask 实现 PermissionResolver。
func (r *httpPermissionResolver) Mask(ctx context.Context, uid string) (*big.Int, string, error) {
	if strings.TrimSpace(uid) == "" {
		return nil, "", fmt.Errorf("%w: uid 为空", ErrPermissionUnavailable)
	}

	if entry, ok := r.lookupMask(uid); ok {
		return new(big.Int).Set(entry.mask), entry.dataScope, nil
	}

	mask, scope, err := r.fetchMask(ctx, uid)
	if err != nil {
		return nil, "", err
	}

	r.storeMask(uid, mask, scope)
	return new(big.Int).Set(mask), scope, nil
}

// BitOf 实现 PermissionResolver。
func (r *httpPermissionResolver) BitOf(ctx context.Context, permCode string) (int, error) {
	dict, err := r.permDict(ctx)
	if err != nil {
		return 0, err
	}
	idx, ok := dict[permCode]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownPermission, permCode)
	}
	return idx, nil
}

func (r *httpPermissionResolver) lookupMask(uid string) (maskEntry, bool) {
	r.maskMu.Lock()
	defer r.maskMu.Unlock()
	entry, ok := r.maskCache[uid]
	if !ok || !r.now().Before(entry.expiresAt) {
		return maskEntry{}, false
	}
	return entry, true
}

func (r *httpPermissionResolver) storeMask(uid string, mask *big.Int, scope string) {
	r.maskMu.Lock()
	defer r.maskMu.Unlock()
	r.maskCache[uid] = maskEntry{
		mask:      new(big.Int).Set(mask),
		dataScope: scope,
		expiresAt: r.now().Add(r.ttl),
	}
}

func (r *httpPermissionResolver) permDict(ctx context.Context) (map[string]int, error) {
	r.dictMu.Lock()
	if r.dictCache != nil && r.now().Before(r.dictCache.expiresAt) {
		dict := r.dictCache.dict
		r.dictMu.Unlock()
		return dict, nil
	}
	r.dictMu.Unlock()

	dict, err := r.fetchPermDict(ctx)
	if err != nil {
		return nil, err
	}

	r.dictMu.Lock()
	r.dictCache = &dictEntry{dict: dict, expiresAt: r.now().Add(r.ttl)}
	r.dictMu.Unlock()
	return dict, nil
}

type maskResponse struct {
	Code int `json:"code"`
	Data struct {
		UID        string `json:"uid"`
		Mask       string `json:"mask"`
		DataScopes []struct {
			ScopeType int `json:"scope_type"`
		} `json:"data_scopes"`
	} `json:"data"`
}

func (r *httpPermissionResolver) fetchMask(ctx context.Context, uid string) (*big.Int, string, error) {
	u, err := url.Parse(r.myMaskURL)
	if err != nil {
		return nil, "", fmt.Errorf("%w: 构造 mask URL 失败: %v", ErrPermissionUnavailable, err)
	}
	q := u.Query()
	q.Set("app_code", r.appCode)
	u.RawQuery = q.Encode()

	body, err := r.doGet(ctx, u.String())
	if err != nil {
		return nil, "", err
	}

	var resp maskResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("%w: 解析 mask 响应失败: %v", ErrPermissionUnavailable, err)
	}

	mask, err := parseHexMask(resp.Data.Mask)
	if err != nil {
		return nil, "", fmt.Errorf("%w: 非法的十六进制掩码", ErrPermissionUnavailable)
	}
	scope := normalizeDataScope(resp.Data.DataScopes)
	return mask, scope, nil
}

type permissionsResponse struct {
	Code int `json:"code"`
	Data []struct {
		Key string `json:"key"`
		Idx int    `json:"idx"`
	} `json:"data"`
}

func (r *httpPermissionResolver) fetchPermDict(ctx context.Context) (map[string]int, error) {
	// 调用 UniAuth /api/meta/permissions 接口
	baseURL := r.myMaskURL
	if idx := strings.Index(baseURL, "/api/user/mask"); idx >= 0 {
		baseURL = baseURL[:idx]
	}
	u := strings.TrimRight(baseURL, "/") + "/api/meta/permissions?app_code=" + r.appCode

	body, err := r.doGet(ctx, u)
	if err != nil {
		return nil, err
	}

	var resp permissionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: 解析 permissions 响应失败: %v", ErrPermissionUnavailable, err)
	}

	dict := make(map[string]int, len(resp.Data))
	for _, p := range resp.Data {
		if p.Key != "" {
			dict[p.Key] = p.Idx
		}
	}
	return dict, nil
}

func (r *httpPermissionResolver) doGet(ctx context.Context, endpoint string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: 构造请求失败: %v", ErrPermissionUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	if token := tokenFromContext(ctx); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: 请求 UniAuth 失败: %v", ErrPermissionUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return nil, fmt.Errorf("%w: UniAuth 返回非 200 状态: %d", ErrPermissionUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: 读取 UniAuth 响应失败: %v", ErrPermissionUnavailable, err)
	}
	return body, nil
}

func parseHexMask(hexStr string) (*big.Int, error) {
	s := strings.TrimSpace(hexStr)
	if s == "" {
		return new(big.Int), nil
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return new(big.Int), nil
	}
	mask, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil, fmt.Errorf("非法十六进制掩码")
	}
	return mask, nil
}

func normalizeDataScope(scopes []struct {
	ScopeType int `json:"scope_type"`
}) string {
	maxScope := 0
	for _, s := range scopes {
		if s.ScopeType > maxScope {
			maxScope = s.ScopeType
		}
	}
	switch maxScope {
	case 3:
		return "ALL"
	case 2:
		return "DEPT"
	case 4:
		return "CUSTOM"
	default:
		return "SELF"
	}
}

type permTokenCtxKey struct{}

// WithToken 将当前请求的 Bearer token 注入上下文。
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, permTokenCtxKey{}, token)
}

func tokenFromContext(ctx context.Context) string {
	t, _ := ctx.Value(permTokenCtxKey{}).(string)
	return t
}
