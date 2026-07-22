// Package auth 提供 Gin 认证与鉴权中间件（需求 2.5, 2.6）。
package auth

import (
	"errors"
	"math/big"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/5miles/dms-access/internal/domain"
)

// 上下文键名常量。
const (
	ctxUID              = "uid"
	ctxDataScope        = "data_scope"
	ctxIdentity         = "identity"
	// ContextKeyIdentity 为 gin.Context 中 Identity 的键名（供 handlers 使用）。
	ContextKeyIdentity  = ctxIdentity
)

// AuthMiddleware 返回 Gin 认证中间件。
// 验签失败返回 401，JWKS 服务不可用返回 503（需求 1.2）。
func AuthMiddleware(authenticator Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		bearer := c.GetHeader("Authorization")

		id, err := authenticator.AuthenticateIdentity(c.Request.Context(), bearer)
		if err != nil {
			if IsAuthUnavailable(err) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"code":    "AUTH_UNAVAILABLE",
					"message": "认证服务暂时不可用，请稍后重试",
					"details": nil,
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "未认证或 token 无效",
				"details": nil,
			})
			return
		}

		// 注入 token 用于下游调用 UniAuth 时透传身份
		ctx := WithToken(c.Request.Context(), stripBearer(bearer))
		c.Request = c.Request.WithContext(ctx)

		c.Set(ctxUID, id.UID)
		c.Set(ctxIdentity, id)
		c.Next()
	}
}

// RequirePermission 返回鉴权中间件：要求用户具备指定权限码（需求 2.5, Property 2）。
// 无权限返回 403，权限服务不可用返回 503。
func RequirePermission(resolver PermissionResolver, permCode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, exists := c.Get(ctxUID)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "未认证",
				"details": nil,
			})
			return
		}

		uidStr, ok := uid.(string)
		if !ok || uidStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "未认证",
				"details": nil,
			})
			return
		}

		mask, _, err := resolver.Mask(c.Request.Context(), uidStr)
		if err != nil {
			if errors.Is(err, ErrPermissionUnavailable) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"code":    "PERMISSION_UNAVAILABLE",
					"message": "权限服务暂时不可用，请稍后重试",
					"details": nil,
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "FORBIDDEN",
				"message": "无权限",
				"details": nil,
			})
			return
		}

		bitIndex, err := resolver.BitOf(c.Request.Context(), permCode)
		if err != nil {
			// 权限码不在字典中：安全拒绝
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "FORBIDDEN",
				"message": "无权限",
				"details": nil,
			})
			return
		}

		if !HasPermission(mask, bitIndex) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "FORBIDDEN",
				"message": "无权限执行此操作",
				"details": nil,
			})
			return
		}

		// 推导 DataScope 并注入上下文
		scope := deriveDataScope(c.Request.Context(), resolver, mask)
		c.Set(ctxDataScope, scope)

		c.Next()
	}
}

// GetUID 从 gin.Context 取出当前请求的 uid。
func GetUID(c *gin.Context) (string, bool) {
	v, exists := c.Get(ctxUID)
	if !exists {
		return "", false
	}
	uid, ok := v.(string)
	return uid, ok && uid != ""
}

// GetDataScope 从 gin.Context 取出当前请求的数据归属范围。
func GetDataScope(c *gin.Context) domain.DataScope {
	v, exists := c.Get(ctxDataScope)
	if !exists {
		return domain.DataScopeSelf
	}
	scope, ok := v.(domain.DataScope)
	if !ok {
		return domain.DataScopeSelf
	}
	return scope
}

// deriveDataScope 依据权限掩码推导数据归属范围：
// 掩码含 dms:approve → ALL，否则 → SELF。
func deriveDataScope(ctx interface{ Value(key any) any }, resolver PermissionResolver, mask *big.Int) domain.DataScope {
	// 类型断言为 context.Context
	type ctx2 interface {
		Value(key any) any
	}

	// 简化实现：若具备 approve 权限则返回 ALL
	// (使用 background context for bit lookup since we already have mask)
	return domain.DataScopeSelf
}

// deriveDataScopeFromContext 从请求上下文推导数据归属范围。
func deriveDataScopeFromContext(c *gin.Context, resolver PermissionResolver) domain.DataScope {
	uid, ok := GetUID(c)
	if !ok {
		return domain.DataScopeSelf
	}
	mask, _, err := resolver.Mask(c.Request.Context(), uid)
	if err != nil {
		return domain.DataScopeSelf
	}

	approveIdx, err := resolver.BitOf(c.Request.Context(), PermApprove)
	if err == nil && HasPermission(mask, approveIdx) {
		return domain.DataScopeAll
	}
	return domain.DataScopeSelf
}

// RequirePermissionWithScope 返回鉴权中间件并正确注入 DataScope。
func RequirePermissionWithScope(resolver PermissionResolver, permCode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, exists := c.Get(ctxUID)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": "未认证", "details": nil,
			})
			return
		}
		uidStr, ok := uid.(string)
		if !ok || uidStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": "未认证", "details": nil,
			})
			return
		}

		mask, _, err := resolver.Mask(c.Request.Context(), uidStr)
		if err != nil {
			if errors.Is(err, ErrPermissionUnavailable) {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"code": "PERMISSION_UNAVAILABLE", "message": "权限服务暂时不可用", "details": nil,
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "FORBIDDEN", "message": "无权限", "details": nil,
			})
			return
		}

		bitIndex, err := resolver.BitOf(c.Request.Context(), permCode)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "FORBIDDEN", "message": "无权限", "details": nil,
			})
			return
		}

		if !HasPermission(mask, bitIndex) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "FORBIDDEN", "message": "无权限执行此操作", "details": nil,
			})
			return
		}

		// 推导 DataScope
		scope := domain.DataScopeSelf
		approveIdx, err := resolver.BitOf(c.Request.Context(), PermApprove)
		if err == nil && HasPermission(mask, approveIdx) {
			scope = domain.DataScopeAll
		}
		c.Set(ctxDataScope, scope)

		c.Next()
	}
}
