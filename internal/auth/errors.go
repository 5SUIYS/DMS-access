package auth

import "errors"

// 认证相关错误。
var (
	// ErrNoToken 表示请求未携带任何 token。
	ErrNoToken = errors.New("auth: 未提供 token")
	// ErrInvalidToken 表示 token 格式非法或验签失败。
	ErrInvalidToken = errors.New("auth: token 无效")
	// ErrExpiredToken 表示 token 已过期。
	ErrExpiredToken = errors.New("auth: token 已过期")
	// ErrEmptyUID 表示 token 中 uid 字段为空。
	ErrEmptyUID = errors.New("auth: uid 为空")
	// ErrKeyNotFound 表示 kid 对应的公钥在 JWKS 中不存在。
	ErrKeyNotFound = errors.New("auth: JWKS 中未找到对应公钥")
	// ErrAuthUnavailable 表示认证服务（JWKS）暂时不可用。
	ErrAuthUnavailable = errors.New("auth: 认证服务暂时不可用")
)

// IsAuthUnavailable 判断 err 是否为 ErrAuthUnavailable。
func IsAuthUnavailable(err error) bool {
	return errors.Is(err, ErrAuthUnavailable)
}
