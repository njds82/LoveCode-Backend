package security

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type mockCloser struct {
	bytes.Buffer
	closed bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return nil
}

func TestRedactStringPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		omits    string
	}{
		{
			name:     "Bearer token",
			input:    "Authorization: Bearer mySecretToken12345678",
			contains: "Authorization: Bearer [REDACTED]",
			omits:    "mySecretToken12345678",
		},
		{
			name:     "AWS Access Key ID AKIA",
			input:    "Found key AKIAIOSFODNN7EXAMPLE in config",
			contains: "Found key [REDACTED] in config",
			omits:    "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:     "AWS Access Key ID ASIA",
			input:    "Temp credentials ASIAIOSFODNN7EXAMPLE",
			contains: "Temp credentials [REDACTED]",
			omits:    "ASIAIOSFODNN7EXAMPLE",
		},
		{
			name:     "AWS Secret Access Key",
			input:    `aws_secret_access_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
			contains: `aws_secret_access_key="[REDACTED]"`,
			omits:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:     "URL Basic Auth",
			input:    "Clone from https://alice:supersecret123@github.com/org/repo.git",
			contains: "https://[REDACTED]@github.com/org/repo.git",
			omits:    "alice:supersecret123",
		},
		{
			name: "JWT token",
			input: "Token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
				"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ." +
				"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			contains: "Token: [REDACTED]",
			omits:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:     "GitHub PAT",
			input:    "Token github_pat_" + strings.Repeat("A1b2C3d4E5", 9)[:82] + " verified",
			contains: "Token [REDACTED] verified",
			omits:    "github_pat_",
		},
		{
			name:     "GitHub Classic Token",
			input:    "ghp_" + strings.Repeat("a", 36),
			contains: "[REDACTED]",
			omits:    "ghp_",
		},
		{
			name:     "GitHub User Token",
			input:    "ghu_" + strings.Repeat("b", 36),
			contains: "[REDACTED]",
			omits:    "ghu_",
		},
		{
			name:     "Slack Token",
			input:    "xoxb-1234567890-1234567890123-abcdefghijklmnop",
			contains: "[REDACTED]",
			omits:    "xoxb-1234567890",
		},
		{
			name:     "LoveCode token",
			input:    "lc_" + strings.Repeat("x", 25),
			contains: "[REDACTED]",
			omits:    "lc_",
		},
		{
			name:     "OpenAI key",
			input:    "sk-" + strings.Repeat("y", 20),
			contains: "[REDACTED]",
			omits:    "sk-",
		},
		{
			name:     "Google key",
			input:    "AIza" + strings.Repeat("z", 32),
			contains: "[REDACTED]",
			omits:    "AIza",
		},
		{
			name:     "Groq key",
			input:    "gsk_" + strings.Repeat("w", 22),
			contains: "[REDACTED]",
			omits:    "gsk_",
		},
		{
			name:     "HuggingFace token",
			input:    "hf_" + strings.Repeat("k", 22),
			contains: "[REDACTED]",
			omits:    "hf_",
		},
		{
			name: "PEM RSA Private Key",
			input: "-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEowIBAAKCAQEA0Y3...\n" +
				"more_key_material...\n" +
				"-----END RSA PRIVATE KEY-----",
			contains: "[REDACTED]",
			omits:    "MIIEowIBAAKCAQEA0Y3",
		},
		{
			name: "PEM EC Private Key",
			input: "-----BEGIN EC PRIVATE KEY-----\n" +
				"MHcCAQEEI...\n" +
				"-----END EC PRIVATE KEY-----",
			contains: "[REDACTED]",
			omits:    "MHcCAQEEI",
		},
		{
			name: "PEM Generic Private Key",
			input: "-----BEGIN PRIVATE KEY-----\n" +
				"MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQD\n" +
				"-----END PRIVATE KEY-----",
			contains: "[REDACTED]",
			omits:    "MIIEvgIBADAN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := RedactString(tt.input)
			if !strings.Contains(out, tt.contains) {
				t.Errorf("expected output to contain %q, got %q", tt.contains, out)
			}
			if strings.Contains(out, tt.omits) {
				t.Errorf("expected output to omit %q, but leaked in %q", tt.omits, out)
			}
		})
	}
}

func TestRedactValueStructured(t *testing.T) {
	data := map[string]any{
		"message":    "hello world",
		"auth_token": "secret_token_value",
		"nested": map[string]any{
			"api_password": "mypassword123",
			"description":  "a public description",
			"raw_secret":   "sk-" + strings.Repeat("a", 20),
		},
		"items": []any{
			"safe text",
			"ghp_" + strings.Repeat("z", 36),
		},
		"string_slice": []string{
			"plain",
			"xoxb-1234567890-1234567890123-abcdefghijklmnop",
		},
	}

	redacted := RedactValue(data).(map[string]any)

	if redacted["message"] != "hello world" {
		t.Fatalf("expected message preserved, got %v", redacted["message"])
	}
	if redacted["auth_token"] != Redacted {
		t.Fatalf("expected auth_token redacted, got %v", redacted["auth_token"])
	}

	nested := redacted["nested"].(map[string]any)
	if nested["api_password"] != Redacted {
		t.Fatalf("expected api_password redacted, got %v", nested["api_password"])
	}
	if nested["description"] != "a public description" {
		t.Fatalf("expected description preserved, got %v", nested["description"])
	}
	if nested["raw_secret"] != Redacted {
		t.Fatalf("expected raw_secret redacted, got %v", nested["raw_secret"])
	}

	tokens := redacted["items"].([]any)
	if tokens[0] != "safe text" {
		t.Fatalf("expected tokens[0] preserved, got %v", tokens[0])
	}
	if tokens[1] != Redacted {
		t.Fatalf("expected tokens[1] redacted, got %v", tokens[1])
	}

	strSlice := redacted["string_slice"].([]string)
	if strSlice[0] != "plain" {
		t.Fatalf("expected strSlice[0] preserved, got %v", strSlice[0])
	}
	if strSlice[1] != Redacted {
		t.Fatalf("expected strSlice[1] redacted, got %v", strSlice[1])
	}
}

func TestRedactingWriterChunkBoundary(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRedactingWriter(&buf)
	rw.SetBufferLimits(32, 256) // small window for testing boundary split

	part1 := "token=sk-" + strings.Repeat("a", 10)
	part2 := strings.Repeat("b", 10) + " trailing data\n"

	// Write part 1 (does not complete the secret)
	if _, err := rw.Write([]byte(part1)); err != nil {
		t.Fatal(err)
	}

	// Write part 2 (completes the secret across chunk boundary)
	if _, err := rw.Write([]byte(part2)); err != nil {
		t.Fatal(err)
	}

	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, "sk-") {
		t.Fatalf("secret leaked across chunk boundary: %q", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("expected [REDACTED] in output, got: %q", out)
	}
	if !strings.Contains(out, "trailing data") {
		t.Fatalf("expected trailing data preserved, got: %q", out)
	}
}

func TestRedactingWriterMultilinePEM(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRedactingWriter(&buf)
	rw.SetBufferLimits(32, 1024)

	pem1 := "-----BEGIN RSA PRIVATE KEY-----\nline1 of key material\n"
	pem2 := "line2 of key material\n-----END RSA PRIVATE KEY-----\n"

	if _, err := rw.Write([]byte(pem1)); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte(pem2)); err != nil {
		t.Fatal(err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, "key material") {
		t.Fatalf("PEM key material leaked: %q", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("expected [REDACTED] for PEM key, got: %q", out)
	}
}

func TestRedactingWriterClose(t *testing.T) {
	mc := &mockCloser{}
	rw := NewRedactingWriter(mc)

	input := "Bearer secret_access_token_12345"
	if _, err := rw.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	if !mc.closed {
		t.Fatal("expected underlying writer to be closed")
	}
	if strings.Contains(mc.String(), "secret_access_token") {
		t.Fatalf("secret leaked: %s", mc.String())
	}
	if !strings.Contains(mc.String(), Redacted) {
		t.Fatalf("expected [REDACTED], got: %s", mc.String())
	}
}

func TestRedactingWriterConcurrent(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRedactingWriter(&buf)

	var wg sync.WaitGroup
	workers := 10
	linesPerWorker := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < linesPerWorker; j++ {
				line := fmt.Sprintf("worker=%d iter=%d key=sk-%016d\n", id, j, j)
				_, _ = rw.Write([]byte(line))
			}
		}(i)
	}

	wg.Wait()
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if strings.Contains(out, "sk-000") {
		t.Fatalf("concurrent writes leaked secrets: %s", out)
	}
	redactedCount := strings.Count(out, Redacted)
	if redactedCount != workers*linesPerWorker {
		t.Fatalf("expected %d redacted tokens, got %d", workers*linesPerWorker, redactedCount)
	}
}
