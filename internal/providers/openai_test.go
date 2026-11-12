package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAPIKey = "sk-test-secret-value-1234567890"

func newOpenAI(t *testing.T, baseURL string, mutate func(*OpenAIConfig)) *OpenAI {
	t.Helper()
	cfg := OpenAIConfig{
		ID: "openai-test", BaseURL: baseURL, APIKey: testAPIKey,
		Timeout: 2 * time.Second, HealthTimeout: time.Second,
		ModelMap: map[string]string{"lc-model": "upstream-model"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewOpenAI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func TestOpenAIRequestConstructionAndResponse(t *testing.T) {
	var gotPath, gotAuth, gotCustom string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("server body: %v", err)
		}
		w.Header().Set("X-Request-ID", "header-request-id")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"id":"body-request-id","model":"upstream-model",
			"choices":[{"message":{"role":"assistant","content":"hello",
			  "tool_calls":[{"id":"call-1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
			  "finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,
			  "prompt_tokens_details":{"cached_tokens":2}}
		}`)
	}))
	defer srv.Close()

	p := newOpenAI(t, srv.URL, func(c *OpenAIConfig) {
		c.DefaultHeaders = map[string]string{"X-Custom": "yes"}
	})
	temp, topP, maxTokens := 0.5, 0.8, 128
	seed := int64(7)
	req := ChatRequest{
		Model:       "lc-model",
		Messages:    []Message{TextMessage(RoleSystem, "be brief"), TextMessage(RoleUser, "hi")},
		Temperature: &temp, TopP: &topP, MaxTokens: &maxTokens, Stop: []string{"END"}, Seed: &seed,
		Tools:      []Tool{{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &ToolChoice{Mode: ToolChoiceAuto},
	}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer "+testAPIKey || gotCustom != "yes" {
		t.Fatalf("path/auth/header: %q %q %q", gotPath, gotAuth, gotCustom)
	}
	if gotBody["model"] != "upstream-model" || gotBody["temperature"] != 0.5 || gotBody["top_p"] != 0.8 {
		t.Fatalf("normalized body: %v", gotBody)
	}
	if gotBody["max_tokens"] != float64(128) || gotBody["seed"] != float64(7) {
		t.Fatalf("max tokens/seed: %v", gotBody)
	}
	if _, ok := gotBody["stream"]; ok {
		t.Fatalf("non-stream request must not include stream: %v", gotBody)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["content"] != "hi" {
		t.Fatalf("messages: %v", msgs)
	}
	if _, ok := gotBody["tools"]; !ok {
		t.Fatalf("tools missing: %v", gotBody)
	}
	if gotBody["tool_choice"] != "auto" {
		t.Fatalf("tool_choice: %v", gotBody["tool_choice"])
	}

	if resp.ProviderID != "openai-test" || resp.Model != "upstream-model" || resp.ProviderRequestID != "body-request-id" {
		t.Fatalf("response identity: %+v", resp)
	}
	if resp.Message.Text() != "hello" || len(resp.Message.ToolCalls) != 1 ||
		resp.Message.ToolCalls[0].Name != "get_weather" || resp.Message.ToolCalls[0].Arguments != `{"city":"Paris"}` {
		t.Fatalf("message: %+v", resp.Message)
	}
	if resp.FinishReason != FinishToolCalls {
		t.Fatalf("finish: %s", resp.FinishReason)
	}
	if resp.Usage == nil || *resp.Usage.PromptTokens != 10 || *resp.Usage.CompletionTokens != 5 ||
		*resp.Usage.TotalTokens != 15 || resp.Usage.CachedPromptTokens == nil || *resp.Usage.CachedPromptTokens != 2 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
	if resp.Timing.Duration <= 0 {
		t.Fatalf("timing: %+v", resp.Timing)
	}
}

func TestOpenAIMissingUsageIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"x","model":"m","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	resp, err := p.Chat(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage != nil {
		t.Fatalf("missing usage became non-nil: %+v", resp.Usage)
	}
	if resp.ProviderRequestID != "x" {
		t.Fatalf("request id: %q", resp.ProviderRequestID)
	}
}

func TestOpenAIErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		code       string
		want       Kind
		retryAfter string
	}{
		{"auth", 401, "invalid_api_key", KindAuthentication, ""},
		{"invalid", 400, "invalid_request_error", KindInvalidRequest, ""},
		{"model", 404, "model_not_found", KindModelNotFound, ""},
		{"rate", 429, "rate_limit_exceeded", KindRateLimited, "3"},
		{"quota", 429, "insufficient_quota", KindQuotaExceeded, ""},
		{"unavailable", 503, "overloaded", KindUnavailable, ""},
		{"internal", 500, "server_error", KindInternal, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "provider-req")
				if c.retryAfter != "" {
					w.Header().Set("Retry-After", c.retryAfter)
				}
				w.WriteHeader(c.status)
				fmt.Fprintf(w, `{"error":{"message":"upstream says no","code":%q}}`, c.code)
			}))
			defer srv.Close()
			p := newOpenAI(t, srv.URL, nil)
			_, err := p.Chat(context.Background(), validRequest())
			if !IsKind(err, c.want) {
				t.Fatalf("want %s got %v", c.want, err)
			}
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("not a provider error: %T", err)
			}
			if pe.HTTPStatus != c.status || pe.ProviderCode != c.code || pe.ProviderRequestID != "provider-req" {
				t.Fatalf("metadata: %+v", pe)
			}
			if c.retryAfter != "" && pe.RetryAfter != 3*time.Second {
				t.Fatalf("retry after: %v", pe.RetryAfter)
			}
		})
	}
}

func TestOpenAIErrorSecretRedaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"bad key %s","code":"invalid_api_key"}}`, testAPIKey)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	_, err := p.Chat(context.Background(), validRequest())
	if err == nil || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestOpenAIMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	if _, err := p.Chat(context.Background(), validRequest()); !IsKind(err, KindMalformedResponse) {
		t.Fatalf("malformed: %v", err)
	}
}

func TestOpenAITimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, `{"choices":[]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, func(c *OpenAIConfig) { c.Timeout = 30 * time.Millisecond })
	start := time.Now()
	_, err := p.Chat(context.Background(), validRequest())
	if !IsKind(err, KindTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout did not stop the request promptly")
	}
}

func TestOpenAICancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()
	p := newOpenAI(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Chat(ctx, validRequest())
		errCh <- err
	}()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if !IsKind(err, KindCanceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel within 1s")
	}
}

func TestOpenAIListModelsAndHealth(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/models" {
			t.Errorf("path: %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"model-a","owned_by":"test","created":123}]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	list, err := p.ListModels(context.Background())
	if err != nil || len(list) != 1 || list[0].ProviderModelID != "model-a" {
		t.Fatalf("list: %v %v", list, err)
	}
	if list[0].Streaming != "unknown" || list[0].ToolCalls != "unknown" {
		t.Fatalf("discovered capabilities must be unknown: %+v", list[0])
	}
	if list[0].Metadata["owned_by"] != "test" {
		t.Fatalf("metadata: %+v", list[0])
	}
	if err := p.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestOpenAIHealthSkip(t *testing.T) {
	p := newOpenAI(t, "http://127.0.0.1:1", func(c *OpenAIConfig) { c.SkipHealthCheck = true })
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("skipped health: %v", err)
	}
}

func TestOpenAIStreaming(t *testing.T) {
	var body map[string]any
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, chunk := range []string{
			`{"id":"r1","choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":"{\"x\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fl.Flush()
		}
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	stream, err := p.ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if accept != "text/event-stream" {
		t.Fatalf("accept: %q", accept)
	}
	if body["stream"] != true {
		t.Fatalf("stream missing: %v", body)
	}
	so, ok := body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options: %v", body["stream_options"])
	}

	var text strings.Builder
	var args strings.Builder
	var done *StreamEvent
	for {
		ev, err := stream.Recv(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case StreamTextDelta:
			text.WriteString(ev.Text)
		case StreamToolCallDelta:
			args.WriteString(ev.ToolCall.ArgumentsDelta)
		case StreamDone:
			e := ev
			done = &e
		}
	}
	if text.String() != "Hello" || args.String() != `{"x":1}` {
		t.Fatalf("stream content: text=%q args=%q", text.String(), args.String())
	}
	if done == nil || done.FinishReason != FinishToolCalls || done.Usage == nil || *done.Usage.TotalTokens != 3 {
		t.Fatalf("done: %+v", done)
	}
	if done.Timing == nil || done.Timing.FirstTokenAt == nil {
		t.Fatalf("stream timing: %+v", done.Timing)
	}
}

func TestOpenAIStreamingMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {oops\n\n")
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	stream, err := p.ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(context.Background()); !IsKind(err, KindMalformedResponse) {
		t.Fatalf("malformed stream: %v", err)
	}
}

func TestOpenAIStreamingWithoutDoneMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	stream, err := p.ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if ev, err := stream.Recv(context.Background()); err != nil || ev.Text != "x" {
		t.Fatalf("delta: %+v %v", ev, err)
	}
	ev, err := stream.Recv(context.Background())
	if err != nil || ev.Type != StreamDone || ev.FinishReason != FinishStop {
		t.Fatalf("done: %+v %v", ev, err)
	}
	if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("eof: %v", err)
	}
}

func TestOpenAIStreamingCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		fl.Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := p.ChatStream(ctx, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if ev, err := stream.Recv(ctx); err != nil || ev.Text != "a" {
		t.Fatalf("first: %+v %v", ev, err)
	}
	<-started
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := stream.Recv(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !IsKind(err, KindCanceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not cancel within 1s")
	}
}

func TestOpenAICustomPathsHeadersAndRawAuth(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("api-key")
		io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, func(c *OpenAIConfig) {
		c.ModelsPath = "/custom/models"
		c.AuthHeader = "api-key"
		c.AuthScheme = "none"
	})
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if path != "/custom/models" || auth != testAPIKey {
		t.Fatalf("custom: %q %q", path, auth)
	}
}

func TestOpenAIProviderOptionsAndReserved(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	req := validRequest()
	req.ProviderOptions = map[string]any{"service_tier": "auto"}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if body["service_tier"] != "auto" {
		t.Fatalf("provider option missing: %v", body)
	}
	req = validRequest()
	req.ProviderOptions = map[string]any{"model": "other"}
	if _, err := p.Chat(context.Background(), req); err == nil {
		t.Fatal("reserved provider option accepted")
	}
}

func TestOpenAIHealthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"no","code":"invalid_api_key"}}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	if err := p.Health(context.Background()); !IsKind(err, KindAuthentication) {
		t.Fatalf("health: %v", err)
	}
}

func TestOpenAIEndpointValidation(t *testing.T) {
	bad := []OpenAIConfig{
		{ID: "x", BaseURL: "ftp://host"},
		{ID: "x", BaseURL: "http://user:pass@host"},
		{ID: "x", BaseURL: "http://host", DefaultHeaders: map[string]string{"X": "a\r\nb"}},
		{ID: "x", BaseURL: "http://host", MaxTokensField: "bogus"},
	}
	for i, cfg := range bad {
		if _, err := NewOpenAI(cfg); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

// Ensure request validation happens before any network call.
func TestOpenAIRejectsInvalidBeforeNetwork(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	req := validRequest()
	req.Model = ""
	if _, err := p.Chat(context.Background(), req); err == nil {
		t.Fatal("expected validation error")
	}
	if called {
		t.Fatal("server was called for an invalid request")
	}
}

func TestOpenAIStreamingHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Header().Set("Retry-After", "1")
		io.WriteString(w, `{"error":{"message":"slow","code":"rate_limit_exceeded"}}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	if _, err := p.ChatStream(context.Background(), validRequest()); !IsKind(err, KindRateLimited) {
		t.Fatalf("stream http error: %v", err)
	}
}

func TestOpenAIConcurrentRequests(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	p := newOpenAI(t, srv.URL, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Chat(context.Background(), validRequest()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxActive > 8 {
		t.Fatalf("max active %d", maxActive)
	}
}
