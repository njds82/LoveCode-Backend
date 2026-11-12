package providers

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newOllama(t *testing.T, baseURL string, mutate func(*OllamaConfig)) *Ollama {
	t.Helper()
	cfg := OllamaConfig{ID: "local", BaseURL: baseURL, Timeout: 2 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := NewOllama(cfg)
	if err != nil {
		t.Fatalf("new ollama: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func TestNewOllamaValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  OllamaConfig
	}{
		{name: "missing id", cfg: OllamaConfig{BaseURL: "http://127.0.0.1:1"}},
		{name: "invalid id", cfg: OllamaConfig{ID: "Bad ID", BaseURL: "http://127.0.0.1:1"}},
		{name: "invalid url", cfg: OllamaConfig{ID: "local", BaseURL: "://bad"}},
		{name: "bad chat path", cfg: OllamaConfig{ID: "local", ChatPath: "api/chat"}},
		{name: "bad tags path", cfg: OllamaConfig{ID: "local", TagsPath: "api/tags"}},
		{name: "bad version path", cfg: OllamaConfig{ID: "local", VersionPath: "api/version"}},
		{name: "empty map key", cfg: OllamaConfig{ID: "local", ModelMap: map[string]string{"": "x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewOllama(tt.cfg); !IsKind(err, KindInvalidConfig) {
				t.Fatalf("want invalid config, got %v", err)
			}
		})
	}
}

func TestOllamaDefaults(t *testing.T) {
	p, err := NewOllama(OllamaConfig{ID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if p.cfg.BaseURL != "http://127.0.0.1:11434" {
		t.Fatalf("base url: %q", p.cfg.BaseURL)
	}
	if p.cfg.ChatPath != "/api/chat" || p.cfg.TagsPath != "/api/tags" || p.cfg.VersionPath != "/api/version" {
		t.Fatalf("paths: %+v", p.cfg)
	}
	if p.Info().Kind != "ollama" || p.Info().DisplayName != "local" {
		t.Fatalf("info: %+v", p.Info())
	}
	caps := p.Capabilities()
	if caps.Streaming != "supported" || caps.ModelListing != "supported" || caps.HealthCheck != "supported" {
		t.Fatalf("capabilities: %+v", caps)
	}
}

func TestOllamaListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tags" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"models":[
			{"name":"llama3:latest","model":"llama3:latest","modified_at":"2026-01-02T03:04:05Z","size":1234,"digest":"sha256:abc",
			 "details":{"format":"gguf","family":"llama","parameter_size":"8B","quantization_level":"Q4_0"}},
			{"model":"fallback-name"},
			{"name":""}
		]}`)
	}))
	defer srv.Close()
	p := newOllama(t, srv.URL, nil)
	list, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("models: %+v", list)
	}
	if list[0].ProviderModelID != "llama3:latest" || list[0].DisplayName != "llama3:latest" {
		t.Fatalf("model 0: %+v", list[0])
	}
	if list[0].Streaming != "unknown" || list[0].ToolCalls != "unknown" || list[0].Reasoning != "unknown" {
		t.Fatalf("capabilities must be unknown: %+v", list[0])
	}
	if list[0].Metadata["family"] != "llama" || list[0].Metadata["size_bytes"] != "1234" || list[0].Metadata["format"] != "gguf" {
		t.Fatalf("metadata: %+v", list[0].Metadata)
	}
	if list[0].Metadata["modified_at"] != "2026-01-02T03:04:05Z" || list[0].Metadata["digest"] != "sha256:abc" {
		t.Fatalf("metadata: %+v", list[0].Metadata)
	}
	if list[1].ProviderModelID != "fallback-name" {
		t.Fatalf("fallback model: %+v", list[1])
	}
}

func TestOllamaListModelsErrors(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"models":`)
		}))
		defer srv.Close()
		p := newOllama(t, srv.URL, nil)
		if _, err := p.ListModels(context.Background()); !IsKind(err, KindMalformedResponse) {
			t.Fatalf("malformed: %v", err)
		}
	})
	t.Run("error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":"boom"}`)
		}))
		defer srv.Close()
		p := newOllama(t, srv.URL, nil)
		_, err := p.ListModels(context.Background())
		if !IsKind(err, KindInternal) || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("status: %v", err)
		}
	})
}

func TestOllamaHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path: %s", r.URL.Path)
		}
		io.WriteString(w, `{"version":"0.5.0"}`)
	}))
	defer srv.Close()
	p := newOllama(t, srv.URL, nil)
	if err := p.Health(context.Background()); err != nil {
		t.Fatal(err)
	}

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"not found"}`)
	}))
	defer notFound.Close()
	if err := newOllama(t, notFound.URL, nil).Health(context.Background()); !IsKind(err, KindUnavailable) {
		t.Fatalf("404 health: %v", err)
	}
}

func TestOllamaChat(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		io.WriteString(w, `{"model":"llama3:latest","created_at":"2026-01-02T03:04:05Z",
			"message":{"role":"assistant","content":"hello","tool_calls":[
				{"function":{"name":"lookup","arguments":{"q":"x"}}}]},
			"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7,
			"total_duration":1000,"load_duration":200,"prompt_eval_duration":300,"eval_duration":400}`)
	}))
	defer srv.Close()
	p := newOllama(t, srv.URL, func(c *OllamaConfig) {
		c.ModelMap = map[string]string{"lovecode-chat": "llama3:latest"}
		c.KeepAlive = "5m"
	})
	req := validRequest()
	req.Model = "lovecode-chat"
	req.Temperature = floatPtr(0.2)
	req.TopP = floatPtr(0.9)
	req.MaxTokens = intPtr(64)
	req.Seed = int64Ptr(42)
	req.Stop = []string{"END"}
	req.ResponseFormat = &ResponseFormat{Type: FormatJSONObject}
	req.ProviderOptions = map[string]any{"num_ctx": 4096}

	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "llama3:latest" || body["stream"] != false || body["keep_alive"] != "5m" {
		t.Fatalf("envelope: %+v", body)
	}
	if body["format"] != "json" {
		t.Fatalf("format: %+v", body["format"])
	}
	options, _ := body["options"].(map[string]any)
	if options["temperature"] != 0.2 || options["top_p"] != 0.9 || options["num_predict"] != float64(64) {
		t.Fatalf("options: %+v", options)
	}
	if options["seed"] != float64(42) {
		t.Fatalf("seed: %+v", options["seed"])
	}
	if body["num_ctx"] != float64(4096) {
		t.Fatalf("provider options: %+v", body)
	}
	if resp.ProviderID != "local" || resp.Model != "llama3:latest" {
		t.Fatalf("response identity: %+v", resp)
	}
	if resp.Message.Text() != "hello" || len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("message: %+v", resp.Message)
	}
	if resp.Message.ToolCalls[0].Name != "lookup" || resp.Message.ToolCalls[0].Arguments != `{"q":"x"}` {
		t.Fatalf("tool call: %+v", resp.Message.ToolCalls[0])
	}
	if resp.FinishReason != FinishStop {
		t.Fatalf("finish: %s", resp.FinishReason)
	}
	if resp.Usage == nil || *resp.Usage.PromptTokens != 5 || *resp.Usage.CompletionTokens != 7 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
	if resp.Metadata["total_duration_ns"] != "1000" || resp.Metadata["eval_duration_ns"] != "400" {
		t.Fatalf("metadata: %+v", resp.Metadata)
	}
	if resp.Timing.CompletedAt.Before(resp.Timing.StartedAt) {
		t.Fatalf("timing: %+v", resp.Timing)
	}
}

func TestOllamaChatMissingUsageIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":{"content":"ok"},"done":true,"done_reason":"length"}`)
	}))
	defer srv.Close()
	resp, err := newOllama(t, srv.URL, nil).Chat(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage != nil {
		t.Fatalf("usage must be nil when absent: %+v", resp.Usage)
	}
	if resp.FinishReason != FinishLength {
		t.Fatalf("finish: %s", resp.FinishReason)
	}
}

func TestOllamaChatValidationAndUnsupported(t *testing.T) {
	p := newOllama(t, "http://127.0.0.1:1", nil)
	if _, err := p.Chat(context.Background(), ChatRequest{}); !IsKind(err, KindInvalidRequest) {
		t.Fatalf("empty request: %v", err)
	}
	req := validRequest()
	req.ToolChoice = &ToolChoice{Mode: ToolChoiceRequired}
	if _, err := p.Chat(context.Background(), req); !IsKind(err, KindUnsupported) {
		t.Fatalf("required tool choice: %v", err)
	}
	req = validRequest()
	req.ProviderOptions = map[string]any{"stream": true}
	if _, err := p.Chat(context.Background(), req); !IsKind(err, KindInvalidRequest) {
		t.Fatalf("conflicting option: %v", err)
	}
	req = validRequest()
	req.Messages = []Message{{Role: RoleUser, Content: []ContentPart{{Type: PartAudio, Audio: &AudioSource{Data: "AAAA"}}}}}
	if _, err := p.Chat(context.Background(), req); !IsKind(err, KindUnsupported) {
		t.Fatalf("audio part: %v", err)
	}
}

func TestOllamaConnectionFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	p := newOllama(t, "http://"+addr, func(c *OllamaConfig) { c.Timeout = 500 * time.Millisecond })
	if err := p.Health(context.Background()); !IsKind(err, KindConnection) {
		t.Fatalf("connection: %v", err)
	}
}

func TestOllamaChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["stream"] != true {
			t.Errorf("stream flag: %+v", body)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		for _, line := range []string{
			`{"model":"llama3","message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"model":"llama3","message":{"role":"assistant","content":"lo"},"done":false}`,
			`{"model":"llama3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":2}`,
		} {
			io.WriteString(w, line+"\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()
	st, err := newOllama(t, srv.URL, nil).ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var events []StreamEvent
	for {
		ev, err := st.Recv(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}
	if len(events) != 3 {
		t.Fatalf("events: %+v", events)
	}
	if events[0].Type != StreamTextDelta || events[0].Text != "Hel" || events[1].Text != "lo" {
		t.Fatalf("deltas: %+v", events)
	}
	done := events[2]
	if done.Type != StreamDone || done.FinishReason != FinishStop {
		t.Fatalf("done: %+v", done)
	}
	if done.Usage == nil || *done.Usage.PromptTokens != 3 || *done.Usage.CompletionTokens != 2 {
		t.Fatalf("usage: %+v", done.Usage)
	}
	if done.Timing == nil || done.Timing.FirstTokenAt == nil || done.Timing.Duration < 0 {
		t.Fatalf("timing: %+v", done.Timing)
	}
	if _, err := st.Recv(context.Background()); err != io.EOF {
		t.Fatalf("after done: %v", err)
	}
}

func TestOllamaChatStreamToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"lookup","arguments":{"q":"x"}}},{"function":{"name":"other","arguments":"{}"}}]},"done":true,"done_reason":"tool_calls"}`+"\n")
	}))
	defer srv.Close()
	st, err := newOllama(t, srv.URL, nil).ChatStream(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first, err := st.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done, err := st.Recv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != StreamToolCallDelta || first.ToolCall == nil || first.ToolCall.Index != 0 || first.ToolCall.Name != "lookup" {
		t.Fatalf("first: %+v", first)
	}
	if first.ToolCall.ArgumentsDelta != `{"q":"x"}` {
		t.Fatalf("arguments: %+v", first.ToolCall)
	}
	if second.ToolCall == nil || second.ToolCall.Index != 1 || second.ToolCall.Name != "other" {
		t.Fatalf("second: %+v", second)
	}
	if done.Type != StreamDone || done.FinishReason != FinishToolCalls {
		t.Fatalf("done: %+v", done)
	}
}

func TestOllamaChatStreamErrors(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "{not json}\n")
		}))
		defer srv.Close()
		st, err := newOllama(t, srv.URL, nil).ChatStream(context.Background(), validRequest())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.Recv(context.Background()); !IsKind(err, KindMalformedResponse) {
			t.Fatalf("malformed: %v", err)
		}
	})
	t.Run("stream error chunk", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"error":"model crashed"}`+"\n")
		}))
		defer srv.Close()
		st, err := newOllama(t, srv.URL, nil).ChatStream(context.Background(), validRequest())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		_, err = st.Recv(context.Background())
		if !IsKind(err, KindInternal) || !strings.Contains(err.Error(), "model crashed") {
			t.Fatalf("error chunk: %v", err)
		}
	})
	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"denied"}`)
		}))
		defer srv.Close()
		_, err := newOllama(t, srv.URL, nil).ChatStream(context.Background(), validRequest())
		if !IsKind(err, KindAuthentication) {
			t.Fatalf("http error: %v", err)
		}
	})
}

func TestOllamaChatStreamCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		io.WriteString(w, `{"message":{"content":"first"},"done":false}`+"\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	st, err := newOllama(t, srv.URL, nil).ChatStream(ctx, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ev, err := st.Recv(context.Background())
	if err != nil || ev.Text != "first" {
		t.Fatalf("first event: %+v %v", ev, err)
	}
	cancel()
	if _, err := st.Recv(context.Background()); !IsKind(err, KindCanceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
