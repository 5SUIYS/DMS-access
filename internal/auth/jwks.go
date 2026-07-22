package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKSCache 管理 UniAuth 公钥的进程内缓存。
type JWKSCache interface {
	// GetKey 按 kid 返回公钥；本地缺失时从 JWKS 端点拉取（带超时）并在进程
	// 生命周期内缓存。拉取失败或超时返回 ErrAuthUnavailable；
	// 刷新后仍无匹配 kid 返回 ErrKeyNotFound。
	GetKey(ctx context.Context, kid string) (crypto.PublicKey, error)
}

// jwk 为 JSON Web Key 中我们关心的字段（RSA 公钥）。
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"` // modulus，base64url
	E   string `json:"e"` // exponent，base64url
}

// jwksDocument 为 JWKS 端点返回的文档结构。
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// httpJWKSCache 是 JWKSCache 的默认实现，从 HTTP JWKS 端点拉取并缓存公钥。
type httpJWKSCache struct {
	endpoint string
	timeout  time.Duration
	client   *http.Client
	observer Observer
	logger   *slog.Logger

	mu   sync.RWMutex
	keys map[string]crypto.PublicKey
}

// JWKSCacheOption 用于配置 httpJWKSCache（便于测试注入短超时/自定义 client）。
type JWKSCacheOption func(*httpJWKSCache)

// WithHTTPClient 注入自定义 http.Client（测试可注入指向 mock 端点的 client）。
func WithHTTPClient(c *http.Client) JWKSCacheOption {
	return func(h *httpJWKSCache) {
		if c != nil {
			h.client = c
		}
	}
}

// WithObserver 注入可观测性挂钩。
func WithObserver(o Observer) JWKSCacheOption {
	return func(h *httpJWKSCache) {
		if o != nil {
			h.observer = o
		}
	}
}

// WithLogger 注入结构化日志器。
func WithLogger(l *slog.Logger) JWKSCacheOption {
	return func(h *httpJWKSCache) {
		if l != nil {
			h.logger = l
		}
	}
}

// NewJWKSCache 创建一个从 endpoint 拉取公钥、带 timeout 超时的 JWKS 缓存。
// endpoint 通常来自 config.JWKSEndpoint()，timeout 来自 config.UniAuth.JWKSTimeout（默认 5s）。
func NewJWKSCache(endpoint string, timeout time.Duration, opts ...JWKSCacheOption) JWKSCache {
	h := &httpJWKSCache{
		endpoint: endpoint,
		timeout:  timeout,
		client:   &http.Client{},
		observer: NopObserver(),
		logger:   slog.Default(),
		keys:     make(map[string]crypto.PublicKey),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// GetKey 实现 JWKSCache。
func (h *httpJWKSCache) GetKey(ctx context.Context, kid string) (crypto.PublicKey, error) {
	// 1. 先读缓存命中（进程生命周期内缓存）。
	if key, ok := h.lookup(kid); ok {
		return key, nil
	}

	// 2. 本地缺失 → 从 JWKS 端点拉取并缓存。
	if err := h.refresh(ctx); err != nil {
		// 拉取失败或超时 → 认证不可用，可观测。
		h.observer.IncAuthUnavailable()
		h.logger.WarnContext(ctx, "JWKS 拉取失败，认证暂时不可用",
			slog.String("endpoint", h.endpoint), slog.Any("error", err))
		return nil, fmt.Errorf("%w: %v", ErrAuthUnavailable, err)
	}

	// 3. 刷新后再查；仍缺失说明该 kid 不属于当前 JWKS。
	if key, ok := h.lookup(kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("%w: kid=%q", ErrKeyNotFound, kid)
}

// lookup 在读锁下查缓存。
func (h *httpJWKSCache) lookup(kid string) (crypto.PublicKey, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	key, ok := h.keys[kid]
	return key, ok
}

// refresh 从 JWKS 端点拉取全部公钥并写入缓存，带超时。
func (h *httpJWKSCache) refresh(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	start := time.Now()
	err := h.doRefresh(fetchCtx)
	h.observer.ObserveJWKSFetch(time.Since(start), err)
	return err
}

func (h *httpJWKSCache) doRefresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.endpoint, nil)
	if err != nil {
		return fmt.Errorf("构造 JWKS 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 JWKS 端点失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 限制读取避免异常大响应。
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		return fmt.Errorf("JWKS 端点返回非 200 状态: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取 JWKS 响应失败: %w", err)
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("解析 JWKS JSON 失败: %w", err)
	}

	parsed := make(map[string]crypto.PublicKey, len(doc.Keys))
	for i := range doc.Keys {
		k := doc.Keys[i]
		if k.Kty != "RSA" {
			continue // 仅支持 RS256 所需的 RSA 公钥。
		}
		if k.Kid == "" {
			continue
		}
		pub, err := k.toRSAPublicKey()
		if err != nil {
			h.logger.WarnContext(ctx, "跳过无法解析的 JWK",
				slog.String("kid", k.Kid), slog.Any("error", err))
			continue
		}
		parsed[k.Kid] = pub
	}

	if len(parsed) == 0 {
		return fmt.Errorf("JWKS 端点未返回任何可用的 RSA 公钥")
	}

	// 合并写入缓存（进程生命周期内保留已有 kid，容纳密钥轮换）。
	h.mu.Lock()
	for kid, key := range parsed {
		h.keys[kid] = key
	}
	h.mu.Unlock()
	return nil
}

// toRSAPublicKey 将 JWK 的 n/e（base64url）转换为 *rsa.PublicKey。
func (j jwk) toRSAPublicKey() (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil {
		return nil, fmt.Errorf("解析 modulus(n) 失败: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(j.E)
	if err != nil {
		return nil, fmt.Errorf("解析 exponent(e) 失败: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, fmt.Errorf("modulus 或 exponent 为空")
	}

	n := new(big.Int).SetBytes(nBytes)

	// exponent 为大端整数，左侧补零到 8 字节后解析。
	var eBuf [8]byte
	copy(eBuf[8-len(eBytes):], eBytes)
	e := binary.BigEndian.Uint64(eBuf[:])
	if e == 0 {
		return nil, fmt.Errorf("非法的 exponent(e)=0")
	}

	return &rsa.PublicKey{N: n, E: int(e)}, nil
}
