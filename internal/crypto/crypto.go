// Package crypto 提供 AES-GCM 加密/解密能力，用于数据源密码的安全存储（需求 3.2）。
//
// 设计约束：
//   - 使用随机 nonce（每次加密不同）防止重放攻击；
//   - 密文以 base64url 格式存储（nonce + ciphertext 拼接后编码）；
//   - 密钥长度须为 16/24/32 字节（AES-128/192/256）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrDecryptFailed 表示解密失败（密文损坏、密钥不匹配等）。
var ErrDecryptFailed = errors.New("crypto: 解密失败")

// Encrypt 使用 AES-GCM 加密 plaintext，返回 base64url 编码的密文（nonce 前缀）。
// key 须为 16、24 或 32 字节（对应 AES-128/192/256）。
// 每次加密使用随机 nonce，因此相同明文每次产生不同密文（需求 3.2）。
func Encrypt(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: 创建 AES cipher 失败: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: 创建 GCM 失败: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: 生成随机 nonce 失败: %w", err)
	}

	// 密文 = Seal(nonce 前缀, nonce, 明文, nil) → nonce || ciphertext+tag
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt 解密由 Encrypt 生成的 base64url 密文，返回明文字符串。
// key 须与加密时使用的 key 完全相同。
func Decrypt(key []byte, ciphertext string) (string, error) {
	data, err := base64.URLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: base64 解码失败: %v", ErrDecryptFailed, err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: 创建 AES cipher 失败: %v", ErrDecryptFailed, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("%w: 创建 GCM 失败: %v", ErrDecryptFailed, err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("%w: 密文过短", ErrDecryptFailed)
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("%w: GCM 解密失败: %v", ErrDecryptFailed, err)
	}

	return string(plaintext), nil
}
