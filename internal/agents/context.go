package agents

import (
	"fmt"
	"strings"

	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/security"
)

const (
	DefaultMaxContextBytes = 32 * 1024 // 32 KB per agent prompt context
	DefaultMaxMessageBytes = 8 * 1024  // 8 KB per individual message
)

type ContextManager struct {
	maxContextBytes int
	maxMessageBytes int
}

func NewContextManager() *ContextManager {
	return &ContextManager{
		maxContextBytes: DefaultMaxContextBytes,
		maxMessageBytes: DefaultMaxMessageBytes,
	}
}

type ContextInput struct {
	Agent       AgentDefinition
	TaskInput   string
	SharedInfo  string
	HandoffInfo string
	Summaries   []string
	Messages    []AgentMessage
}

// BuildMessages constructs a bounded slice of providers.Message values according to the agent's context policy.
func (cm *ContextManager) BuildMessages(ci ContextInput) []providers.Message {
	var msgs []providers.Message

	// 1. System Prompt / Instructions
	sysPrompt := cm.buildSystemPrompt(ci.Agent)
	if sysPrompt != "" {
		msgs = append(msgs, providers.TextMessage(providers.RoleSystem, cm.boundString(sysPrompt, cm.maxMessageBytes)))
	}

	// 2. Policy-specific context injection
	policy := ci.Agent.ContextPolicy
	if policy == "" {
		policy = ContextIsolated
	}

	switch policy {
	case ContextIsolated:
		// Isolated: Only explicit task input provided, no shared or predecessor context
		if ci.TaskInput != "" {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(ci.TaskInput, cm.maxMessageBytes)))
		}

	case ContextShared:
		// Shared: Access explicitly shared run context and broadcast messages
		var sb strings.Builder
		if ci.SharedInfo != "" {
			sb.WriteString("### Shared Run Context:\n")
			sb.WriteString(ci.SharedInfo)
			sb.WriteString("\n\n")
		}
		if len(ci.Messages) > 0 {
			sb.WriteString("### Team Messages:\n")
			for _, m := range ci.Messages {
				if m.ToAgentID == "" || m.ToAgentID == ci.Agent.ID {
					sb.WriteString(fmt.Sprintf("[%s -> %s] (%s): %s\n", m.FromAgentID, m.ToAgentID, m.Type, m.Content))
				}
			}
			sb.WriteString("\n")
		}
		if sb.Len() > 0 {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(sb.String(), cm.maxMessageBytes)))
		}
		if ci.TaskInput != "" {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(ci.TaskInput, cm.maxMessageBytes)))
		}

	case ContextHandoff:
		// Handoff: Input handed off directly from predecessor task
		if ci.HandoffInfo != "" {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString("### Predecessor Output Handoff:\n"+ci.HandoffInfo, cm.maxMessageBytes)))
		}
		if ci.TaskInput != "" {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(ci.TaskInput, cm.maxMessageBytes)))
		}

	case ContextSummarized:
		// Summarized: Bounded summary of previous task outputs
		if len(ci.Summaries) > 0 {
			var sb strings.Builder
			sb.WriteString("### Previous Results Summary:\n")
			for i, s := range ci.Summaries {
				sb.WriteString(fmt.Sprintf("- Summary %d: %s\n", i+1, s))
			}
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(sb.String(), cm.maxMessageBytes)))
		}
		if ci.TaskInput != "" {
			msgs = append(msgs, providers.TextMessage(providers.RoleUser, cm.boundString(ci.TaskInput, cm.maxMessageBytes)))
		}
	}

	return msgs
}

func (cm *ContextManager) buildSystemPrompt(a AgentDefinition) string {
	var parts []string
	if a.Role != "" {
		parts = append(parts, fmt.Sprintf("Role: %s", a.Role))
	}
	if a.Description != "" {
		parts = append(parts, fmt.Sprintf("Description: %s", a.Description))
	}
	if a.SystemPrompt != "" {
		parts = append(parts, a.SystemPrompt)
	}
	return strings.Join(parts, "\n\n")
}

func (cm *ContextManager) boundString(s string, maxBytes int) string {
	s = security.RedactString(s)
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n...[truncated due to context size limit]"
}
