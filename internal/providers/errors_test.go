package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestKindForStatus(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   Kind
	}{
		{400, "", KindInvalidRequest},
		{401, "", KindAuthentication},
		{403, "", KindAuthentication},
		{404, "", KindModelNotFound},
		{405, "", KindUnsupported},
		{408, "", KindTimeout},
		{429, "", KindRateLimited},
		{429, "insufficient_quota", KindQuotaExceeded},
		{500, "", KindInternal},
		{502, "", KindUnavailable},
		{503, "", KindUnavailable},
		{504, "", KindTimeout},
		{529, "", KindUnavailable},
	}
	for _, c := range cases {
		if got := KindForStatus(c.status, c.code); got != c.want {
			t.Errorf("status %d code %q: got %s want %s", c.status, c.code, got, c.want)
		}
	}
}

func TestFromStatusRedactsSecrets(t *testing.T) {
	e := FromStatus("p", 401, "invalid_api_key", "bad key sk-supersecretvalue1234567890", 3*time.Second, "sk-supersecretvalue1234567890")
	if strings.Contains(e.Error(), "supersecret") {
		t.Fatalf("secret leaked: %s", e.Error())
	}
	if e.Kind != KindAuthentication || e.HTTPStatus != 401 || e.RetryAfter != 3*time.Second {
		t.Fatalf("metadata lost: %+v", e)
	}
	if !IsKind(e, KindAuthentication) {
		t.Fatal("IsKind failed")
	}
	wrapped := errors.Join(errors.New("outer"), e)
	if !IsKind(wrapped, KindAuthentication) {
		t.Fatal("IsKind should find wrapped error")
	}
}

func TestErrorMessageAndJSONHaveNoCause(t *testing.T) {
	e := FromStatus("p", 500, "", "Bearer abcdefghijklmnop", 0, "")
	e.cause = errors.New("internal cause with secret hunter2")
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "abcdefghijklmnop") {
		t.Fatalf("sensitive data in JSON: %s", raw)
	}
	if strings.Contains(e.Error(), "hunter2") {
		t.Fatal("cause printed by Error()")
	}
}

func TestFromTransportContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if e := FromTransport(canceled, "p", context.Canceled); e.Kind != KindCanceled {
		t.Fatalf("canceled: %+v", e)
	}
	deadline, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel2()
	_ = deadline.Err()
	if e := FromTransport(deadline, "p", context.DeadlineExceeded); e.Kind != KindTimeout {
		t.Fatalf("deadline: %+v", e)
	}
	u := &url.Error{Op: "Post", URL: "https://example.invalid/v1?api_key=secret", Err: errors.New("dial failed")}
	e := FromTransport(context.Background(), "p", u)
	if e.Kind != KindConnection {
		t.Fatalf("connection: %+v", e)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Fatalf("URL query leaked: %s", e.Error())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := ParseRetryAfter("2", now); got != 2*time.Second {
		t.Fatalf("seconds: %v", got)
	}
	if got := ParseRetryAfter("1.5", now); got != 1500*time.Millisecond {
		t.Fatalf("fractional: %v", got)
	}
	if got := ParseRetryAfter(now.Add(10*time.Second).Format(time.RFC1123), now); got != 10*time.Second {
		t.Fatalf("http date: %v", got)
	}
	if got := ParseRetryAfter("garbage", now); got != 0 {
		t.Fatalf("garbage: %v", got)
	}
}

func TestSanitizeText(t *testing.T) {
	in := "error at https://host/v1?token=sekrit&x=1\nnext line\x00"
	out := SanitizeText(in)
	if strings.Contains(out, "sekrit") || strings.Contains(out, "\n") || strings.ContainsRune(out, 0) {
		t.Fatalf("sanitize failed: %q", out)
	}
	long := strings.Repeat("x", maxErrorMessage+100)
	if len(SanitizeText(long)) > maxErrorMessage {
		t.Fatal("message not truncated")
	}
}
