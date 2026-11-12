package providers

import (
	"fmt"
	"strings"

	"github.com/lovecode/lovecode/internal/config"
)

func newOpenAIFromConfig(cfg config.ProviderConfig, apiKey string) (Provider, error) {
	s := newSettingsParser(cfg.ID, cfg.Settings)
	chatPath := s.string("chat_path", "")
	modelsPath := s.string("models_path", "")
	healthPath := s.string("health_path", "")
	skipHealth := s.boolean("skip_health_check", false)
	authHeader := s.string("auth_header", "Authorization")
	authScheme := s.string("auth_scheme", "Bearer")
	maxTokensField := s.string("max_tokens_field", "max_tokens")
	streamUsage, _ := s.optionalBool("stream_usage")
	organization := s.string("organization", "")
	project := s.string("project", "")
	modelMap, err := buildModelMap(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.done(); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}
	p, err := NewOpenAI(OpenAIConfig{
		ID:              cfg.ID,
		DisplayName:     cfg.DisplayName,
		Description:     cfg.Description,
		BaseURL:         cfg.Endpoint,
		APIKey:          apiKey,
		DefaultHeaders:  cfg.DefaultHeaders,
		Timeout:         cfg.TimeoutOrDefault(),
		HealthTimeout:   cfg.HealthTimeoutOrDefault(),
		ChatPath:        chatPath,
		ModelsPath:      modelsPath,
		HealthPath:      healthPath,
		SkipHealthCheck: skipHealth,
		AuthHeader:      authHeader,
		AuthScheme:      authScheme,
		MaxTokensField:  maxTokensField,
		StreamUsage:     streamUsage,
		Organization:    organization,
		Project:         project,
		ModelMap:        modelMap,
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func newOllamaFromConfig(cfg config.ProviderConfig, _ string) (Provider, error) {
	s := newSettingsParser(cfg.ID, cfg.Settings)
	chatPath := s.string("chat_path", "")
	tagsPath := s.string("tags_path", "")
	versionPath := s.string("version_path", "")
	keepAlive := s.string("keep_alive", "")
	modelMap, err := buildModelMap(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.done(); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}
	p, err := NewOllama(OllamaConfig{
		ID:          cfg.ID,
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		BaseURL:     cfg.Endpoint,
		Timeout:     cfg.TimeoutOrDefault(),
		ChatPath:    chatPath,
		TagsPath:    tagsPath,
		VersionPath: versionPath,
		KeepAlive:   keepAlive,
		ModelMap:    modelMap,
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func newMockFromConfig(cfg config.ProviderConfig, _ string) (Provider, error) {
	s := newSettingsParser(cfg.ID, cfg.Settings)
	reply := s.string("reply", "")
	chunkSize := s.integer("chunk_size", 0)
	chunkDelay := s.duration("chunk_delay", 0)
	latency := s.duration("latency", 0)
	simulateTimeout := s.duration("simulate_timeout", 0)
	unavailable := s.boolean("unavailable", false)
	streamErrorAfter := s.integer("stream_error_after", 0)
	finishReason := s.string("finish_reason", "")

	failKind := s.string("fail_kind", "")
	failMessage := s.string("fail_message", "")
	failStatus := s.integer("fail_http_status", 0)
	failCode := s.string("fail_provider_code", "")
	failRetryAfter := s.duration("fail_retry_after", 0)

	streamFailKind := s.string("stream_fail_kind", "")
	streamFailMessage := s.string("stream_fail_message", "")

	promptTokens, hasPrompt := s.optionalInt("prompt_tokens")
	completionTokens, hasCompletion := s.optionalInt("completion_tokens")
	totalTokens, hasTotal := s.optionalInt("total_tokens")

	var models []ModelInfo
	for _, mc := range cfg.Models {
		models = append(models, modelInfoFromConfig(mc))
	}
	if err := s.done(); err != nil {
		return nil, NewError(KindInvalidConfig, cfg.ID, err.Error())
	}

	var failure *Error
	if failKind != "" {
		kind := Kind(failKind)
		if !kind.Valid() {
			return nil, NewError(KindInvalidConfig, cfg.ID, fmt.Sprintf("mock fail_kind %q is invalid", failKind))
		}
		if failMessage == "" {
			failMessage = "simulated provider failure"
		}
		failure = &Error{
			Kind:         kind,
			ProviderID:   cfg.ID,
			Message:      SanitizeText(failMessage),
			HTTPStatus:   failStatus,
			ProviderCode: SanitizeText(failCode),
			RetryAfter:   failRetryAfter,
		}
	}
	var streamError *Error
	if streamFailKind != "" {
		kind := Kind(streamFailKind)
		if !kind.Valid() {
			return nil, NewError(KindInvalidConfig, cfg.ID, fmt.Sprintf("mock stream_fail_kind %q is invalid", streamFailKind))
		}
		if streamFailMessage == "" {
			streamFailMessage = "simulated mid-stream failure"
		}
		streamError = &Error{Kind: kind, ProviderID: cfg.ID, Message: SanitizeText(streamFailMessage)}
	}
	var usage *Usage
	if hasPrompt || hasCompletion || hasTotal {
		usage = &Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: totalTokens}
	}
	if finishReason != "" {
		fr := FinishReason(finishReason)
		switch fr {
		case FinishStop, FinishLength, FinishToolCalls, FinishContentFilter, FinishError, FinishUnknown:
		default:
			return nil, NewError(KindInvalidConfig, cfg.ID, fmt.Sprintf("mock finish_reason %q is invalid", finishReason))
		}
	}
	p, err := NewMock(MockConfig{
		ID:               cfg.ID,
		DisplayName:      cfg.DisplayName,
		Description:      cfg.Description,
		Models:           models,
		Reply:            reply,
		FinishReason:     FinishReason(finishReason),
		Usage:            usage,
		Latency:          latency,
		ChunkDelay:       chunkDelay,
		ChunkSize:        chunkSize,
		Failure:          failure,
		SimulateTimeout:  simulateTimeout,
		Unavailable:      unavailable,
		StreamErrorAfter: streamErrorAfter,
		StreamError:      streamError,
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func buildModelMap(cfg config.ProviderConfig) (map[string]string, error) {
	if len(cfg.Models) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(cfg.Models))
	for _, mc := range cfg.Models {
		id := mc.ID
		if id == "" {
			var err error
			id, err = deriveModelID(cfg.ID, mc.ProviderModelID)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(mc.ProviderModelID) == "" {
			return nil, NewError(KindInvalidConfig, cfg.ID, "provider_model_id must not be empty")
		}
		out[id] = mc.ProviderModelID
	}
	return out, nil
}
