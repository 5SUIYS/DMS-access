package auth_test

// Feature: mysql-redshift-migration-approval, Property 2: 角色权限隔离

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/5miles/dms-access/internal/auth"
)

// TestHasPermission 验证 HasPermission 纯函数的基本行为。
func TestHasPermission(t *testing.T) {
	// bit 0 = access, bit 1 = apply, bit 2 = approve
	mask := new(big.Int).SetBits([]big.Word{0b111}) // bits 0,1,2 全置 1

	assert.True(t, auth.HasPermission(mask, 0), "bit 0 应为 true")
	assert.True(t, auth.HasPermission(mask, 1), "bit 1 应为 true")
	assert.True(t, auth.HasPermission(mask, 2), "bit 2 应为 true")
	assert.False(t, auth.HasPermission(mask, 3), "bit 3 应为 false")

	// nil mask 返回 false
	assert.False(t, auth.HasPermission(nil, 0))

	// 负索引返回 false
	assert.False(t, auth.HasPermission(mask, -1))

	// 空掩码（0）所有位均为 false
	emptyMask := new(big.Int)
	assert.False(t, auth.HasPermission(emptyMask, 0))
}

// TestHasPermission_ApplyOnly 验证仅具 apply 权限的用户无法通过 approve 检查。
func TestHasPermission_ApplyOnly(t *testing.T) {
	// bit 1 = apply only
	mask := new(big.Int).SetBit(new(big.Int), 1, 1)

	assert.True(t, auth.HasPermission(mask, 1), "apply 权限（bit 1）应为 true")
	assert.False(t, auth.HasPermission(mask, 2), "approve 权限（bit 2）不应具备")
	assert.False(t, auth.HasPermission(mask, 0), "access 权限（bit 0）不应具备")
}
