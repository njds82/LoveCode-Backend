package tier2_boundaries

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/lovecode/lovecode/internal/security"
)

// TestBoundaries_Redact_StreamingChunkBoundary_TokenSplit verifies that secrets
// (such as Bearer tokens or GitHub PATs) split directly across write chunk boundaries
// in RedactingWriter are buffered in the tail window and correctly masked upon flush.
func TestBoundaries_Redact_StreamingChunkBoundary_TokenSplit(t *testing.T) {
	var out bytes.Buffer
	rw := security.NewRedactingWriter(&out)
	rw.SetBufferLimits(128, 4096)

	rawSecret := "ghp_123456789012345678901234567890123456"

	// Split secret into two halves
	part1 := "Prefix data before secret: ghp_123456789012345"
	part2 := "678901234567890123456 and suffix data."

	if _, err := rw.Write([]byte(part1)); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	if _, err := rw.Write([]byte(part2)); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	// Close / flush writer
	if err := rw.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	result := out.String()

	// The raw token must NOT appear in output
	if strings.Contains(result, rawSecret) {
		t.Fatalf("security violation: raw token leaked across chunk boundary: %s", result)
	}

	// Redacted marker must be present
	if !strings.Contains(result, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, result)
	}
}

// TestBoundaries_Redact_PEMPrivateKey_MultiLineBoundary verifies that multiline PEM
// private keys spanning multiple streaming write operations are retained in the buffer
// until the closing END marker is reached and completely masked.
func TestBoundaries_Redact_PEMPrivateKey_MultiLineBoundary(t *testing.T) {
	var out bytes.Buffer
	rw := security.NewRedactingWriter(&out)
	rw.SetBufferLimits(256, 8192)

	pemChunks := []string{
		"Config file:\n-----BEGIN RSA PRIVATE",
		" KEY-----\nMIIEowIBAAKCAQEA0Y1+MockKeyDataPart1\n",
		"MockKeyDataPart2123456789==\n-----END RSA PRIVATE KEY-----\nEnd config.",
	}

	for _, chunk := range pemChunks {
		if _, err := rw.Write([]byte(chunk)); err != nil {
			t.Fatalf("write chunk failed: %v", err)
		}
	}

	if err := rw.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	result := out.String()

	if strings.Contains(result, "MockKeyDataPart1") || strings.Contains(result, "MockKeyDataPart2") {
		t.Fatalf("security violation: PEM key body leaked in output: %s", result)
	}

	if !strings.Contains(result, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, result)
	}
}

// TestBoundaries_Redact_HighVolumeSecrets_Masking verifies that large streams containing
// thousands of distinct secrets (JWTs, API keys) are processed without panic or memory leaks.
func TestBoundaries_Redact_HighVolumeSecrets_Masking(t *testing.T) {
	var out bytes.Buffer
	rw := security.NewRedactingWriter(&out)

	const secretCount = 2000
	for i := 0; i < secretCount; i++ {
		line := fmt.Sprintf("entry_%d: key=AKIAIOSFODNN7EXAM%03d\n", i, i%1000)
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatalf("write line %d failed: %v", i, err)
		}
	}

	if err := rw.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	result := out.String()

	// Verify AKIA keys were masked
	if strings.Contains(result, "AKIAIOSFODNN7EXAM") {
		t.Fatalf("security violation: unredacted AWS key detected in high-volume output")
	}

	count := strings.Count(result, security.Redacted)
	if count < secretCount {
		t.Errorf("expected at least %d redacted markers, got %d", secretCount, count)
	}
}

// TestBoundaries_Redact_NestedJSON_SecretsMasking verifies that deeply nested
// JSON objects and arrays with sensitive keys or sensitive values are masked recursively by RedactValue.
func TestBoundaries_Redact_NestedJSON_SecretsMasking(t *testing.T) {
	nestedPayload := map[string]any{
		"service": "billing",
		"config": map[string]any{
			"api_token": "sk-secretKey1234567890abcdef", // sensitive key -> replaced with [REDACTED]
			"database": map[string]any{
				"password": "superSecretPassword123", // sensitive key -> replaced with [REDACTED]
				"host":     "db.internal",            // non-sensitive key & benign value -> preserved
			},
			"credentials": map[string]any{ // sensitive key -> entire object replaced with [REDACTED]
				"issuer": "internal-auth",
			},
			"metadata": map[string]any{ // non-sensitive key
				"note": "my aws key is AKIAIOSFODNN7EXAMPLE in text", // pattern matched -> redacted in string
				"items": []any{
					"safe-public-identifier",
					"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
				},
			},
		},
	}

	redacted := security.RedactValue(nestedPayload)
	asMap, ok := redacted.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", redacted)
	}

	cfg, _ := asMap["config"].(map[string]any)
	if cfg["api_token"] != security.Redacted {
		t.Errorf("expected api_token to be redacted, got: %v", cfg["api_token"])
	}

	db, _ := cfg["database"].(map[string]any)
	if db["password"] != security.Redacted {
		t.Errorf("expected password to be redacted, got: %v", db["password"])
	}
	if db["host"] != "db.internal" {
		t.Errorf("expected benign host to remain intact, got: %v", db["host"])
	}

	// Sensitive key "credentials" must have its value completely masked
	if cfg["credentials"] != security.Redacted {
		t.Errorf("expected credentials object to be replaced with [REDACTED], got: %v", cfg["credentials"])
	}

	meta, _ := cfg["metadata"].(map[string]any)
	noteStr, _ := meta["note"].(string)
	if strings.Contains(noteStr, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("expected AWS key in note to be redacted, got: %s", noteStr)
	}
	if !strings.Contains(noteStr, security.Redacted) {
		t.Errorf("expected note to contain [REDACTED], got: %s", noteStr)
	}

	itemList, _ := meta["items"].([]any)
	if len(itemList) != 2 {
		t.Fatalf("expected 2 items, got %d", len(itemList))
	}
	if itemList[0] != "safe-public-identifier" {
		t.Errorf("expected safe identifier untouched, got: %v", itemList[0])
	}
	item1Str, _ := itemList[1].(string)
	if strings.Contains(item1Str, "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c") {
		t.Errorf("expected Bearer token signature to be redacted, got: %v", itemList[1])
	}
}

// TestBoundaries_Redact_FalsePositiveAvoidance verifies that non-sensitive strings,
// such as UUIDs, HTTP verbs, words containing substrings like "bear" or "token",
// and regular identifiers are not erroneously redacted.
func TestBoundaries_Redact_FalsePositiveAvoidance(t *testing.T) {
	benignStrings := []string{
		"The bear climbed the mountain.",
		"token_count: 42",
		"request_id: 123e4567-e89b-12d3-a456-426614174000",
		"Content-Type: application/json",
		"GET /v1/models HTTP/1.1",
		"user_id: user_1234567890",
		"http://localhost:8080/path/without/credentials",
	}

	for _, s := range benignStrings {
		result := security.RedactString(s)
		if strings.Contains(result, security.Redacted) {
			t.Errorf("false positive: benign string %q was erroneously redacted: %s", s, result)
		}
	}
}

// TestBoundaries_Redact_URLBasicAuth_SpecialCharacters verifies that credentials
// containing special characters (e.g. $, %, !, +) inside URL basic auth are masked.
func TestBoundaries_Redact_URLBasicAuth_SpecialCharacters(t *testing.T) {
	rawURL := "https://service_bot:P%40ssw+rd!$123@api.internal.lovecode.com/v1/sync"
	result := security.RedactString(rawURL)

	if strings.Contains(result, "P%40ssw+rd!$123") {
		t.Fatalf("security violation: password with special characters leaked: %s", result)
	}

	if !strings.Contains(result, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, result)
	}
}
