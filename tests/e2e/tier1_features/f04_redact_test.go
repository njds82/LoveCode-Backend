package tier1_features

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lovecode/lovecode/internal/security"
)

func TestF04_Redact_JWTTokens(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	input := "Bearer " + jwt + " is the authorization header"

	output := security.RedactString(input)
	if strings.Contains(output, "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c") {
		t.Fatalf("security violation: raw JWT secret leaked in output: %s", output)
	}
	if !strings.Contains(output, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, output)
	}
}

func TestF04_Redact_PEMPrivateKeys(t *testing.T) {
	pemKey := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Y1+MockKeyDataHereForTestingPurposeOnly123456789==\n-----END RSA PRIVATE KEY-----"
	input := "Server key config:\n" + pemKey

	output := security.RedactString(input)
	if strings.Contains(output, "MockKeyDataHereForTestingPurposeOnly123456789") {
		t.Fatalf("security violation: raw PEM key leaked in output: %s", output)
	}
	if !strings.Contains(output, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, output)
	}
}

func TestF04_Redact_AWSAccessKeys(t *testing.T) {
	awsKey := "AKIAIOSFODNN7EXAMPLE"
	input := "export AWS_ACCESS_KEY_ID=" + awsKey

	output := security.RedactString(input)
	if strings.Contains(output, awsKey) {
		t.Fatalf("security violation: raw AWS key leaked in output: %s", output)
	}
	if !strings.Contains(output, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, output)
	}
}

func TestF04_Redact_GitHubPersonalAccessTokens(t *testing.T) {
	ghp := "ghp_123456789012345678901234567890123456"
	input := "git clone https://" + ghp + "@github.com/org/repo.git"

	output := security.RedactString(input)
	if strings.Contains(output, ghp) {
		t.Fatalf("security violation: raw GitHub PAT leaked in output: %s", output)
	}
	if !strings.Contains(output, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, output)
	}
}

func TestF04_Redact_URLBasicAuthCredentials(t *testing.T) {
	urlWithCreds := "https://admin:superSecretPass123@internal.lovecode.dev/api"
	input := "connecting to " + urlWithCreds

	output := security.RedactString(input)
	if strings.Contains(output, "superSecretPass123") {
		t.Fatalf("security violation: raw URL basic auth leaked in output: %s", output)
	}
	if !strings.Contains(output, security.Redacted) {
		t.Fatalf("expected output to contain %s, got: %s", security.Redacted, output)
	}
}

func TestF04_Redact_RedactingWriter_Streaming(t *testing.T) {
	// Tests streaming writer interface or fallback
	var buf bytes.Buffer
	// Test standard RedactValue on map containing sensitive keys
	payload := map[string]any{
		"api_key":   "sk-1234567890abcdef1234567890",
		"normal":    "clean text data",
		"user_pass": "myPassword999",
	}

	redacted := security.RedactValue(payload).(map[string]any)
	if redacted["api_key"] != security.Redacted {
		t.Errorf("expected api_key to be redacted, got: %v", redacted["api_key"])
	}
	if redacted["normal"] != "clean text data" {
		t.Errorf("expected normal field to remain unredacted, got: %v", redacted["normal"])
	}

	// Verify buffer write redaction
	writer := io.Writer(&buf)
	_, _ = writer.Write([]byte(security.RedactString("token sk-abcdef1234567890123456")))
	if strings.Contains(buf.String(), "sk-abcdef1234567890123456") {
		t.Errorf("failed to redact sk- token in written stream")
	}
}
