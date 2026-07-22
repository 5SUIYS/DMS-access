package crypto_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5miles/dms-access/internal/crypto"
)

var testKey = []byte("test-key-32-bytes-long-padding!!")

// TestEncryptDecryptRoundTrip 验证 Encrypt→Decrypt 往返一致性。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	testCases := []string{
		"password123",
		"",
		"complex!@#$%^&*()",
		"中文密码",
		"a very long password that exceeds typical lengths to test AES-GCM behavior with larger inputs",
	}
	for _, plaintext := range testCases {
		t.Run("plaintext="+truncate(plaintext, 20), func(t *testing.T) {
			ciphertext, err := crypto.Encrypt(testKey, plaintext)
			require.NoError(t, err)
			assert.NotEmpty(t, ciphertext)

			decrypted, err := crypto.Decrypt(testKey, ciphertext)
			require.NoError(t, err)
			assert.Equal(t, plaintext, decrypted, "往返后明文应与原文完全一致")
		})
	}
}

// TestEncryptNonceRandomness 验证相同明文每次加密产生不同密文（nonce 随机性）。
func TestEncryptNonceRandomness(t *testing.T) {
	plaintext := "same-password"
	const n = 10
	ciphertexts := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		ct, err := crypto.Encrypt(testKey, plaintext)
		require.NoError(t, err)
		assert.False(t, ciphertexts[ct], "第 %d 次加密产生了重复密文", i+1)
		ciphertexts[ct] = true
	}
}

// TestDecryptWrongKey 验证使用错误密钥解密返回错误。
func TestDecryptWrongKey(t *testing.T) {
	ciphertext, err := crypto.Encrypt(testKey, "secret")
	require.NoError(t, err)

	wrongKey := []byte("wrong-key-32-bytes-long-padding!")
	_, err = crypto.Decrypt(wrongKey, ciphertext)
	assert.Error(t, err, "使用错误密钥解密应返回错误")
	assert.ErrorIs(t, err, crypto.ErrDecryptFailed)
}

// TestDecryptInvalidCiphertext 验证解密损坏的密文返回错误。
func TestDecryptInvalidCiphertext(t *testing.T) {
	_, err := crypto.Decrypt(testKey, "not-valid-base64!!!")
	assert.Error(t, err)

	_, err = crypto.Decrypt(testKey, "dGVzdA==") // 太短（"test" base64）
	assert.Error(t, err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
