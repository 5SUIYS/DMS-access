package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Identity 承载验签成功后提取的身份声明（需求 1.3）。
type Identity struct {
	UID         string
	Username    string // 取自 JWT claim（如 "username" / "preferred_username"）
	DisplayName string // 取自 JWT claim（如 "display_name" / "name"），可空
}

// Authenticator 校验 UniAuth 签发的 token 并取出 uid。
type Authenticator interface {
	// Authenticate 校验 bearerToken（可带或不带 "Bearer " 前缀），返回 uid，
	// 或返回认证错误：
	//   - token 缺失/为空 → ErrNoToken（401）
	//   - 验签失败/格式非法/kid 无匹配公钥 → ErrInvalidToken/ErrKeyNotFound（401）
	//   - token 已过期 → ErrExpiredToken（401）
	//   - uid 字段缺失或为空 → ErrEmptyUID（401）
	//   - JWKS 拉取失败或超时 → ErrAuthUnavailable（503）
	Authenticate(ctx context.Context, bearerToken string) (uid string, err error)
	// AuthenticateIdentity 在 Authenticate 基础上额外解析 username / display_name 声明。
	// display_name 可空。其余错误语义与 Authenticate 一致。
	AuthenticateIdentity(ctx context.Context, bearerToken string) (Identity, error)
}

// UIDClaim 为 JWT 中承载用户唯一标识的默认声明名。
const UIDClaim = "uid"

// jwtHeader 为 JWT 头部我们关心的字段。
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// authenticator 是 Authenticator 的默认实现：基于 JWKSCache 做 RS256 本地验签，
// 同时支持本地 HS256 token（kid="local"，用于免 UniAuth 登录）。
type authenticator struct {
	keys           JWKSCache
	appCode        string
	uidClaim       string
	localJWTSecret string // 本地 HS256 密钥；空表示禁用本地登录
	// leeway 为过期校验允许的时钟偏移容差，默认 0（严格按 exp 判定）。
	leeway time.Duration
	// now 便于测试注入固定时间；默认 time.Now。
	now func() time.Time
}

// AuthenticatorOption 用于配置 authenticator。
type AuthenticatorOption func(*authenticator)

// WithClockSkew 设置过期校验的时钟偏移容差（默认 0）。
func WithClockSkew(d time.Duration) AuthenticatorOption {
	return func(a *authenticator) {
		if d > 0 {
			a.leeway = d
		}
	}
}

// WithUIDClaim 自定义承载 uid 的声明名（默认 "uid"）。
func WithUIDClaim(name string) AuthenticatorOption {
	return func(a *authenticator) {
		if strings.TrimSpace(name) != "" {
			a.uidClaim = name
		}
	}
}

// WithLocalJWTSecret 启用本地 HS256 token 验签（免 UniAuth 登录）。
// secret 为空时不启用本地验签，全部走 UniAuth JWKS。
func WithLocalJWTSecret(secret string) AuthenticatorOption {
	return func(a *authenticator) {
		a.localJWTSecret = secret
	}
}

// withClock 注入自定义时钟（测试用）。
func withClock(now func() time.Time) AuthenticatorOption {
	return func(a *authenticator) {
		if now != nil {
			a.now = now
		}
	}
}

// NewAuthenticator 创建一个基于 JWKS 本地验签的 Authenticator。
// DMS 仅使用 UniAuth，appCode 通常为 "dms"。
func NewAuthenticator(keys JWKSCache, appCode string, opts ...AuthenticatorOption) Authenticator {
	a := &authenticator{
		keys:     keys,
		appCode:  appCode,
		uidClaim: UIDClaim,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Authenticate 实现 Authenticator。
// 优先尝试本地 HS256 token（kid="local"），不可用时走 UniAuth JWKS RS256。
func (a *authenticator) Authenticate(ctx context.Context, bearerToken string) (string, error) {
	raw := stripBearer(bearerToken)
	if raw == "" {
		return "", ErrNoToken
	}

	if a.localJWTSecret != "" && isLocalToken(raw) {
		id, err := VerifyLocalJWT(raw, a.localJWTSecret)
		if err != nil {
			return "", err
		}
		return id.UID, nil
	}

	claims, err := a.verifyAndParseClaims(ctx, raw)
	if err != nil {
		return "", err
	}

	return extractUID(claims, a.uidClaim)
}

// AuthenticateIdentity 实现 Authenticator。
// 优先尝试本地 HS256 token（kid="local"），不可用时走 UniAuth JWKS RS256。
// 额外提取 username / display_name 声明；display_name 缺省为空串。
func (a *authenticator) AuthenticateIdentity(ctx context.Context, bearerToken string) (Identity, error) {
	raw := stripBearer(bearerToken)
	if raw == "" {
		return Identity{}, ErrNoToken
	}

	if a.localJWTSecret != "" && isLocalToken(raw) {
		return VerifyLocalJWT(raw, a.localJWTSecret)
	}

	claims, err := a.verifyAndParseClaims(ctx, raw)
	if err != nil {
		return Identity{}, err
	}

	uid, err := extractUID(claims, a.uidClaim)
	if err != nil {
		return Identity{}, err
	}

	username := extractClaimString(claims, "username")
	if username == "" {
		username = extractClaimString(claims, "preferred_username")
	}
	displayName := extractClaimString(claims, "display_name")
	if displayName == "" {
		displayName = extractClaimString(claims, "name")
	}

	return Identity{
		UID:         uid,
		Username:    username,
		DisplayName: displayName,
	}, nil
}

// verifyAndParseClaims 执行 JWT 分段解析、算法校验、取公钥、验签、过期校验，
// 返回解析后的 claims map。任一环节失败均返回对应错误。
func (a *authenticator) verifyAndParseClaims(ctx context.Context, raw string) (map[string]json.RawMessage, error) {
	// JWT 由 header.payload.signature 三段组成。
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: 非 JWT 三段结构", ErrInvalidToken)
	}
	headerSeg, payloadSeg, sigSeg := parts[0], parts[1], parts[2]

	// 1. 解析头部，校验算法与取 kid。
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: 头部 base64 解码失败", ErrInvalidToken)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("%w: 头部 JSON 解析失败", ErrInvalidToken)
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("%w: 不支持的签名算法 %q", ErrInvalidToken, hdr.Alg)
	}

	// 2. 按 kid 取公钥。
	pubKey, err := a.keys.GetKey(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: 公钥类型非 RSA", ErrInvalidToken)
	}

	// 3. 验签：signingInput = header.payload，SHA-256 后做 PKCS#1 v1.5 验证。
	sig, err := base64.RawURLEncoding.DecodeString(sigSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: 签名 base64 解码失败", ErrInvalidToken)
	}
	signingInput := headerSeg + "." + payloadSeg
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, fmt.Errorf("%w: RS256 验签不通过", ErrInvalidToken)
	}

	// 4. 解析载荷并校验过期。
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadSeg)
	if err != nil {
		return nil, fmt.Errorf("%w: 载荷 base64 解码失败", ErrInvalidToken)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: 载荷 JSON 解析失败", ErrInvalidToken)
	}

	if err := a.checkExpiry(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// checkExpiry 校验 exp 声明；缺失或非法视为验签失败（不可判定有效期即拒绝）。
func (a *authenticator) checkExpiry(claims map[string]json.RawMessage) error {
	rawExp, ok := claims["exp"]
	if !ok {
		return fmt.Errorf("%w: 缺少 exp 声明", ErrInvalidToken)
	}
	var expSec float64
	if err := json.Unmarshal(rawExp, &expSec); err != nil {
		return fmt.Errorf("%w: exp 声明非数值", ErrInvalidToken)
	}
	expTime := time.Unix(int64(expSec), 0)
	if !a.now().Add(-a.leeway).Before(expTime) {
		return ErrExpiredToken
	}
	return nil
}

// extractUID 从 claims 中取出 uid（字符串）并校验非空。
func extractUID(claims map[string]json.RawMessage, claimName string) (string, error) {
	raw, ok := claims[claimName]
	if !ok {
		return "", ErrEmptyUID
	}
	// uid 可能是字符串或数值，统一转为字符串处理。
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return "", ErrEmptyUID
		}
		return asString, nil
	}
	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		s := asNumber.String()
		if strings.TrimSpace(s) == "" {
			return "", ErrEmptyUID
		}
		return s, nil
	}
	return "", ErrEmptyUID // uid 非字符串/数值 → 视为无效
}

// extractClaimString 从 claims 中取出指定声明的字符串值；缺失或非法返回空串。
func extractClaimString(claims map[string]json.RawMessage, claimName string) string {
	raw, ok := claims[claimName]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// stripBearer 去除可选的 "Bearer" 前缀并去空白，兼容中间件传入原始头值或纯 token。
// "Bearer"（无实际 token）或空串统一返回空串，交由上层判定为未携带 token。
func stripBearer(token string) string {
	t := strings.TrimSpace(token)
	if t == "" {
		return ""
	}
	const scheme = "bearer"
	if len(t) >= len(scheme) && strings.EqualFold(t[:len(scheme)], scheme) {
		return strings.TrimSpace(t[len(scheme):])
	}
	return t
}
