package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func validRequest() ChatRequest {
	return ChatRequest{Model: "model-1", Messages: []Message{TextMessage(RoleUser, "hello")}}
}

func TestChatRequestValidation(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatalf("base request invalid: %v", err)
	}
	temp := 3.0
	topP := 1.5
	maxTokens := 0
	ok := true
	cases := []struct {
		name   string
		mutate func(*ChatRequest)
	}{
		{"no model", func(r *ChatRequest) { r.Model = "" }},
		{"no messages", func(r *ChatRequest) { r.Messages = nil }},
		{"bad role", func(r *ChatRequest) { r.Messages[0].Role = "root" }},
		{"tool without id", func(r *ChatRequest) { r.Messages = []Message{{Role: RoleTool, Content: []ContentPart{TextPart("x")}}} }},
		{"temperature", func(r *ChatRequest) { r.Temperature = &temp }},
		{"top_p", func(r *ChatRequest) { r.TopP = &topP }},
		{"max tokens", func(r *ChatRequest) { r.MaxTokens = &maxTokens }},
		{"empty stop", func(r *ChatRequest) { r.Stop = []string{""} }},
		{"bad image", func(r *ChatRequest) {
			r.Messages[0].Content = []ContentPart{{Type: PartImage, Image: &ImageSource{URL: "ftp://x"}}}
		}},
		{"bad tool name", func(r *ChatRequest) {
			r.Tools = []Tool{{Name: "bad name", Parameters: json.RawMessage(`{}`)}}
		}},
		{"invalid tool schema", func(r *ChatRequest) {
			r.Tools = []Tool{{Name: "ok", Parameters: json.RawMessage(`{`)}}
		}},
		{"bad tool choice", func(r *ChatRequest) { r.ToolChoice = &ToolChoice{Mode: ToolChoiceTool} }},
		{"schema without name", func(r *ChatRequest) {
			r.ResponseFormat = &ResponseFormat{Type: FormatJSONSchema, Schema: &JSONSchema{Schema: json.RawMessage(`{}`)}}
		}},
		{"reserved provider option", func(r *ChatRequest) { r.ProviderOptions = map[string]any{"temperature": 1} }},
		{"non-json option", func(r *ChatRequest) { r.ProviderOptions = map[string]any{"x": make(chan int)} }},
		{"bad assistant tool args", func(r *ChatRequest) {
			r.Messages = append(r.Messages, Message{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "x", Arguments: "not-json"}}})
		}},
	}
	for _, c := range cases {
		r := validRequest()
		c.mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	// Valid rich request.
	r := validRequest()
	r.Temperature = floatPtr(0.7)
	r.TopP = floatPtr(0.9)
	r.MaxTokens = intPtr(128)
	r.Stop = []string{"END"}
	r.Tools = []Tool{{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(`{"type":"object"}`)}}
	r.ToolChoice = &ToolChoice{Mode: ToolChoiceAuto}
	r.ResponseFormat = &ResponseFormat{Type: FormatJSONSchema, Schema: &JSONSchema{Name: "out", Schema: json.RawMessage(`{"type":"object"}`), Strict: &ok}}
	r.ProviderOptions = map[string]any{"service_tier": "auto"}
	if err := r.Validate(); err != nil {
		t.Fatalf("rich request invalid: %v", err)
	}
}

func TestMessageText(t *testing.T) {
	m := Message{Content: []ContentPart{TextPart("a"), {Type: PartImage, Image: &ImageSource{URL: "https://x/y.png"}}, TextPart("b")}}
	if m.Text() != "ab" {
		t.Fatalf("got %q", m.Text())
	}
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

func TestReservedOptionsAreNotSilentlyMerged(t *testing.T) {
	r := validRequest()
	r.ProviderOptions = map[string]any{"model": "other"}
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved option error, got %v", err)
	}
}
