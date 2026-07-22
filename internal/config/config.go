// Package config 负责从环境变量加载 DMS-access 运行所需的全部配置。
//
// 设计约束：
//   - 敏感项（DATABASE_URL、JWT_SECRET、ENCRYPTION_KEY 等）仅从环境变量读取，绝不硬编码默认值。
//   - 配置缺失时 Validate() 返回包含每个缺失变量名的聚合错误列表（非静默通过，需求 9.3）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// 默认值常量：非敏感项提供合理默认，敏感项无默认。
const (
	// DefaultServerAddr 为 HTTP 服务默认监听地址。
	DefaultServerAddr = ":8080"
	// DefaultJWKSTimeout 为从 JWKS 端点拉取公钥的超时时间。
	DefaultJWKSTimeout = 5 * time.Second
	// DefaultPermCacheTTL 为权限掩码缓存的短 TTL。
	DefaultPermCacheTTL = 30 * time.Second
	// DefaultMaxConns 为连接池默认最大连接数。
	DefaultMaxConns = 10
)

// Config 聚合 DMS-access 全部运行配置。
type Config struct {
	Server     ServerConfig
	Database   DatabaseConfig
	UniAuth    UniAuthConfig
	DMS        DMSConfig
	Encryption EncryptionConfig
}

// ServerConfig 为 HTTP 服务配置。
type ServerConfig struct {
	Addr      string
	// JWTSecret 为 JWT 签名密钥，用于本地签发/校验会话 token（必填）。
	JWTSecret string
}

// DatabaseConfig 为 PostgreSQL 配置。
type DatabaseConfig struct {
	// DSN 为 PostgreSQL 连接串，仅从环境变量 DATABASE_URL 读取。
	DSN      string
	MaxConns int32
}

// UniAuthConfig 为统一认证相关配置。
type UniAuthConfig struct {
	// URL 为 UniAuth 基础地址，JWKS 端点为 ${URL}/.well-known/jwks.json。
	URL            string
	AppCode        string
	MyMaskURL      string
	JWKSTimeout    time.Duration
	PermCacheTTL   time.Duration
}

// DMSConfig 为 AWS DMS 相关配置。
type DMSConfig struct {
	// ReplicationInstanceARN 为常驻 DMS 复制实例 ARN（必填）。
	ReplicationInstanceARN string
	Region                 string
}

// EncryptionConfig 为数据源密码加密配置。
type EncryptionConfig struct {
	// Key 为 AES-GCM 密钥，来自环境变量 ENCRYPTION_KEY（必填）。
	Key string
}

// Load 从环境变量加载配置并校验必填项。
//
// 环境变量映射：
//
//	SERVER_ADDR                   -> Server.Addr（默认 :8080）
//	JWT_SECRET                    -> Server.JWTSecret（必填）
//	DATABASE_URL                  -> Database.DSN（必填）
//	DATABASE_MAX_CONNS            -> Database.MaxConns（默认 10）
//	UNIAUTH_URL                   -> UniAuth.URL（必填）
//	UNIAUTH_APP_CODE              -> UniAuth.AppCode（必填）
//	UNIAUTH_MYMASK_URL            -> UniAuth.MyMaskURL（必填）
//	UNIAUTH_JWKS_TIMEOUT          -> UniAuth.JWKSTimeout（默认 5s）
//	UNIAUTH_PERM_CACHE_TTL        -> UniAuth.PermCacheTTL（默认 30s）
//	DMS_REPLICATION_INSTANCE_ARN  -> DMS.ReplicationInstanceARN（必填）
//	AWS_REGION                    -> DMS.Region（必填）
//	ENCRYPTION_KEY                -> Encryption.Key（必填）
func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Addr:      envString("SERVER_ADDR", DefaultServerAddr),
			JWTSecret: envString("JWT_SECRET", ""),
		},
		Database: DatabaseConfig{
			DSN:      envString("DATABASE_URL", ""),
			MaxConns: int32(envInt("DATABASE_MAX_CONNS", DefaultMaxConns)),
		},
		UniAuth: UniAuthConfig{
			URL:          envString("UNIAUTH_URL", ""),
			AppCode:      envString("UNIAUTH_APP_CODE", ""),
			MyMaskURL:    envString("UNIAUTH_MYMASK_URL", ""),
			JWKSTimeout:  envDuration("UNIAUTH_JWKS_TIMEOUT", DefaultJWKSTimeout),
			PermCacheTTL: envDuration("UNIAUTH_PERM_CACHE_TTL", DefaultPermCacheTTL),
		},
		DMS: DMSConfig{
			ReplicationInstanceARN: envString("DMS_REPLICATION_INSTANCE_ARN", ""),
			Region:                 envString("AWS_REGION", ""),
		},
		Encryption: EncryptionConfig{
			Key: envString("ENCRYPTION_KEY", ""),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 校验必填项，返回包含每个缺失变量名的聚合错误（需求 9.3）。
func (c *Config) Validate() error {
	var missing []string

	if strings.TrimSpace(c.Database.DSN) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if strings.TrimSpace(c.Server.JWTSecret) == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if strings.TrimSpace(c.UniAuth.URL) == "" {
		missing = append(missing, "UNIAUTH_URL")
	}
	if strings.TrimSpace(c.UniAuth.AppCode) == "" {
		missing = append(missing, "UNIAUTH_APP_CODE")
	}
	if strings.TrimSpace(c.UniAuth.MyMaskURL) == "" {
		missing = append(missing, "UNIAUTH_MYMASK_URL")
	}
	if strings.TrimSpace(c.DMS.ReplicationInstanceARN) == "" {
		missing = append(missing, "DMS_REPLICATION_INSTANCE_ARN")
	}
	if strings.TrimSpace(c.DMS.Region) == "" {
		missing = append(missing, "AWS_REGION")
	}
	if strings.TrimSpace(c.Encryption.Key) == "" {
		missing = append(missing, "ENCRYPTION_KEY")
	}

	if len(missing) > 0 {
		return fmt.Errorf("config: 缺少必填环境变量: %s", strings.Join(missing, ", "))
	}

	if c.Database.MaxConns <= 0 {
		return fmt.Errorf("config: DATABASE_MAX_CONNS 必须大于 0")
	}
	if c.UniAuth.JWKSTimeout <= 0 {
		return fmt.Errorf("config: UNIAUTH_JWKS_TIMEOUT 必须大于 0")
	}

	return nil
}

// JWKSEndpoint 返回 JWKS 公钥端点地址。
func (c *Config) JWKSEndpoint() string {
	return strings.TrimRight(c.UniAuth.URL, "/") + "/.well-known/jwks.json"
}

// envString 读取字符串环境变量，缺失或为空白时返回默认值。
func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// envDuration 读取 time.Duration 环境变量，解析失败时返回默认值。
func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// envInt 读取整型环境变量，解析失败时返回默认值。
func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
