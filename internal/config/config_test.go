package config_test

// Feature: mysql-redshift-migration-approval, Property 18: 环境变量缺失启动失败

import (
	"strings"
	"testing"
	"time"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5miles/dms-access/internal/config"
)

// requiredEnvVars 为 DMS-access 必填环境变量列表（需求 9.3）。
var requiredEnvVars = []string{
	"DATABASE_URL",
	"JWT_SECRET",
	"UNIAUTH_URL",
	"UNIAUTH_APP_CODE",
	"UNIAUTH_MYMASK_URL",
	"DMS_REPLICATION_INSTANCE_ARN",
	"AWS_REGION",
	"ENCRYPTION_KEY",
}

// allEnvValues 为测试时为所有必填变量提供的合法值。
var allEnvValues = map[string]string{
	"DATABASE_URL":                  "postgres://user:pass@localhost:5432/dms_test",
	"JWT_SECRET":                    "super-secret-jwt-key-32chars-pad!",
	"UNIAUTH_URL":                   "https://uniauth.example.com",
	"UNIAUTH_APP_CODE":              "dms",
	"UNIAUTH_MYMASK_URL":            "https://uniauth.example.com/api/user/mask",
	"DMS_REPLICATION_INSTANCE_ARN":  "arn:aws:dms:us-east-1:123456789:rep:test-instance",
	"AWS_REGION":                    "us-east-1",
	"ENCRYPTION_KEY":                "test-key-32-bytes-long-padding!!",
}

// TestValidate_AllPresent 验证所有必填变量均存在时 Validate 返回 nil。
func TestValidate_AllPresent(t *testing.T) {
	for k, v := range allEnvValues {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "postgres://user:pass@localhost:5432/dms_test", cfg.Database.DSN)
	assert.Equal(t, "arn:aws:dms:us-east-1:123456789:rep:test-instance", cfg.DMS.ReplicationInstanceARN)
	assert.Equal(t, "super-secret-jwt-key-32chars-pad!", cfg.Server.JWTSecret)
}

// TestValidate_MissingOneByOne 逐一缺失每个必填变量，验证 Validate 返回包含该变量名的错误。
func TestValidate_MissingOneByOne(t *testing.T) {
	for _, missing := range requiredEnvVars {
		t.Run("missing_"+missing, func(t *testing.T) {
			// 设置除当前变量外的所有变量。
			for k, v := range allEnvValues {
				if k != missing {
					t.Setenv(k, v)
				} else {
					t.Setenv(k, "") // 显式设为空以覆盖父测试可能遗留的值
				}
			}
			_, err := config.Load()
			require.Error(t, err, "缺少 %s 时应返回错误", missing)
			assert.Contains(t, err.Error(), missing, "错误信息应包含缺失的变量名 %s", missing)
		})
	}
}

// TestProperty18_MissingSubset 属性测试：对必填变量的任意非空缺失子集，Validate 返回包含各缺失变量名的错误。
// Property 18: 环境变量缺失启动失败
// Validates: Requirements 9.3
func TestProperty18_MissingSubset(t *testing.T) {
	properties := gopter.NewProperties(gopter.DefaultTestParameters())

	properties.Property("Property 18: 环境变量缺失启动失败 — 任意缺失子集均返回含缺失变量名的错误",
		prop.ForAll(
			func(missingIndices []int) bool {
				if len(missingIndices) == 0 {
					return true
				}

				// 去重并限制在有效索引范围内。
				seen := make(map[int]bool)
				var missingKeys []string
				for _, idx := range missingIndices {
					if idx < 0 {
						idx = -idx
					}
					idx = idx % len(requiredEnvVars)
					if !seen[idx] {
						seen[idx] = true
						missingKeys = append(missingKeys, requiredEnvVars[idx])
					}
				}
				if len(missingKeys) == 0 {
					return true
				}

				// 构造 Config 结构体：缺失的变量置空。
				cfg := &config.Config{
					Server: config.ServerConfig{
						Addr:      ":8080",
						JWTSecret: getOrFull("JWT_SECRET", missingKeys),
					},
					Database: config.DatabaseConfig{DSN: getOrFull("DATABASE_URL", missingKeys), MaxConns: 10},
					UniAuth: config.UniAuthConfig{
						URL:          getOrFull("UNIAUTH_URL", missingKeys),
						AppCode:      getOrFull("UNIAUTH_APP_CODE", missingKeys),
						MyMaskURL:    getOrFull("UNIAUTH_MYMASK_URL", missingKeys),
						JWKSTimeout:  5 * time.Second,
						PermCacheTTL: 30 * time.Second,
					},
					DMS: config.DMSConfig{
						ReplicationInstanceARN: getOrFull("DMS_REPLICATION_INSTANCE_ARN", missingKeys),
						Region:                 getOrFull("AWS_REGION", missingKeys),
					},
					Encryption: config.EncryptionConfig{
						Key: getOrFull("ENCRYPTION_KEY", missingKeys),
					},
				}

				err := cfg.Validate()
				if err == nil {
					return false // 有缺失变量时不应返回 nil
				}
				// 验证错误信息包含每个缺失变量名。
				for _, k := range missingKeys {
					if !strings.Contains(err.Error(), k) {
						return false
					}
				}
				return true
			},
			gen.SliceOf(gen.IntRange(0, 100)),
		),
	)

	properties.TestingRun(t)
}

// 辅助：若 key 在 missing 列表中则返回空串，否则返回 allEnvValues 中的值。
func getOrFull(key string, missing []string) string {
	for _, k := range missing {
		if k == key {
			return ""
		}
	}
	return allEnvValues[key]
}
