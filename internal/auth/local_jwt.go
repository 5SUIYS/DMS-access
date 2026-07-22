package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LocalJWTKid 为本地签发 token 的 kid（区别于 UniAuth RS256 的 kid）。
const LocalJWTKid = "local"

// localJWTHeader 为本地 JWT 头部（HS256、kid=local）。
type localJWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// localJWTClaims 为本地 JWT 载荷。
type localJWTClaims struct {
	Sub      string `json:"sub"`      // uid
	Username string `json:"username"` // 登录名
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

// isLocalToken 判断 token 是否为本地 HS256 token（kid="local"）。
func isLocalToken(raw string) bool {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var hdr localJWTHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return false
	}
	return hdr.Kid == LocalJWTKid && hdr.Alg == "HS256"
}

// VerifyLocalJWT 验签本地 HS256 token，返回其中的 Identity。
func VerifyLocalJWT(token, secret string) (Identity, error) {
	raw := token
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		raw = raw[7:]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Identity{}, ErrNoToken
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Identity{}, fmt.Errorf("%w: 非 JWT 三段结构", ErrInvalidToken)
	}
	headerSeg, payloadSeg, sigSeg := parts[0], parts[1], parts[2]

	// 校验 header
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: 头部 base64 解码失败", ErrInvalidToken)
	}
	var hdr localJWTHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return Identity{}, fmt.Errorf("%w: 头部 JSON 解析失败", ErrInvalidToken)
	}
	if hdr.Kid != LocalJWTKid || hdr.Alg != "HS256" {
		return Identity{}, fmt.Errorf("%w: 不支持的 kid/alg", ErrInvalidToken)
	}

	// HMAC-SHA256 验签
	signingInput := headerSeg + "." + payloadSeg
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := base64.RawURLEncoding.DecodeString(sigSeg)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: 签名 base64 解码失败", ErrInvalidToken)
	}
	if !hmac.Equal(expectedSig, actualSig) {
		return Identity{}, fmt.Errorf("%w: HS256 验签不通过", ErrInvalidToken)
	}

	// 解析 claims
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadSeg)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: 载荷 base64 解码失败", ErrInvalidToken)
	}
	var claims localJWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return Identity{}, fmt.Errorf("%w: 载荷 JSON 解析失败", ErrInvalidToken)
	}

	// 过期校验
	if time.Now().Unix() > claims.Exp {
		return Identity{}, ErrExpiredToken
	}

	if claims.Sub == "" {
		return Identity{}, ErrEmptyUID
	}

	return Identity{
		UID:         claims.Sub,
		Username:    claims.Username,
		DisplayName: "",
	}, nil
}
