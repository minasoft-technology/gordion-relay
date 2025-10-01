package timetoken

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TokenPayload represents the data stored in the time-limited token
type TokenPayload struct {
	Exp  int64  `json:"exp"`  // Expiration timestamp (Unix)
	Path string `json:"path"` // Resource path being protected
	Iat  int64  `json:"iat"`  // Issued at timestamp (Unix)
	Jti  string `json:"jti"`  // Unique token ID for replay protection
}

// GenerateToken creates a time-limited encrypted token for the given path
func GenerateToken(apiKey, path string, duration time.Duration) (string, error) {
	now := time.Now().Unix()
	payload := TokenPayload{
		Exp:  now + int64(duration.Seconds()),
		Path: path,
		Iat:  now,
		Jti:  uuid.New().String(),
	}

	// Marshal payload to JSON
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token payload: %w", err)
	}

	// Encrypt the payload using AES-GCM
	encryptedToken, err := encryptAESGCM(payloadBytes, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Base64 URL encode for safe use in URLs
	token := base64.URLEncoding.EncodeToString(encryptedToken)
	return token, nil
}

// ValidateToken decrypts and validates a time-limited token
func ValidateToken(apiKey, token, requestedPath string) error {
	// Base64 URL decode
	encryptedToken, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("invalid token encoding: %w", err)
	}

	// Decrypt the token
	payloadBytes, err := decryptAESGCM(encryptedToken, apiKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt token: %w", err)
	}

	// Unmarshal payload
	var payload TokenPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return fmt.Errorf("invalid token payload: %w", err)
	}

	// Check expiration
	now := time.Now().Unix()
	if now > payload.Exp {
		return fmt.Errorf("token has expired")
	}

	// Check path matches
	if payload.Path != requestedPath {
		return fmt.Errorf("token path mismatch: expected %s, got %s", payload.Path, requestedPath)
	}

	// Token is valid
	return nil
}

// encryptAESGCM encrypts data using AES-GCM with the given key
func encryptAESGCM(data []byte, key string) ([]byte, error) {
	// Create a SHA-256 hash of the key to ensure it's 32 bytes
	keyHash := sha256.Sum256([]byte(key))

	// Create cipher
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, err
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Create nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	// Encrypt and append nonce
	encrypted := gcm.Seal(nonce, nonce, data, nil)
	return encrypted, nil
}

// decryptAESGCM decrypts data using AES-GCM with the given key
func decryptAESGCM(encryptedData []byte, key string) ([]byte, error) {
	// Create a SHA-256 hash of the key to ensure it's 32 bytes
	keyHash := sha256.Sum256([]byte(key))

	// Create cipher
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, err
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Check minimum length
	nonceSize := gcm.NonceSize()
	if len(encryptedData) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	// Extract nonce and ciphertext
	nonce := encryptedData[:nonceSize]
	ciphertext := encryptedData[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// IsTokenRequired determines if a URL needs token validation
// This helps maintain backward compatibility and excludes web UI paths
func IsTokenRequired(requestPath string) bool {
	// Skip token validation for basic endpoints
	if requestPath == "/ping" || requestPath == "/health" || requestPath == "/" {
		return false
	}

	// Skip token validation for web UI API endpoints
	if contains(requestPath, "/api/v1/health/") ||
	   contains(requestPath, "/api/stats/") ||
	   contains(requestPath, "/api/v1/config/") ||
	   contains(requestPath, "/api/health") ||
	   contains(requestPath, "/api/v1/transfers/") ||
	   contains(requestPath, "/api/v1/hl7/") ||
	   contains(requestPath, "/api/v1/system/") ||
	   contains(requestPath, "/api/v1/commands") {
		return false
	}

	// Skip token validation for MinIO URLs (they have their own presigned security)
	if isMinIOURL(requestPath) {
		return false
	}

	// Require tokens for DICOM content endpoints (instances/download, studies, etc.)
	return contains(requestPath, "/instances/") && contains(requestPath, "/download")
}

// isMinIOURL checks if a URL is a MinIO presigned URL
func isMinIOURL(urlStr string) bool {
	// MinIO URLs contain AWS signature parameters
	return contains(urlStr, "X-Amz-Algorithm") &&
		contains(urlStr, "X-Amz-Credential") &&
		contains(urlStr, "X-Amz-Signature")
}

// contains checks if a string contains a substring (case-sensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(substr) == 0 ||
		 indexOf(s, substr) >= 0)
}

// indexOf returns the index of the first occurrence of substr in s, or -1 if not found
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}