package agents

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type AggregatedResult struct {
	FinalText          string         `json:"final_text"`
	StructuredData     map[string]any `json:"structured_data"`
	ContributingAgents []string       `json:"contributing_agents"`
	SuccessfulTasks    []string       `json:"successful_tasks"`
	FailedTasks        []string       `json:"failed_tasks"`
	CancelledTasks     []string       `json:"cancelled_tasks"`
	HasConflicts       bool           `json:"has_conflicts"`
	ConflictDetails    []string       `json:"conflict_details,omitempty"`
}

type Aggregator struct {
	runtime *AgentRuntime
}

func NewAggregator(rt *AgentRuntime) *Aggregator {
	return &Aggregator{runtime: rt}
}

func (ag *Aggregator) Aggregate(
	ctx context.Context,
	policy AggregationPolicy,
	primaryAgentID string,
	synthesizerAgent *AgentDefinition,
	results []*TaskExecutionResult,
) (AggregatedResult, error) {
	out := AggregatedResult{
		StructuredData: make(map[string]any),
	}

	var successful []*TaskExecutionResult
	agentOutputs := make(map[string]string)

	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case StatusCompleted:
			successful = append(successful, r)
			out.SuccessfulTasks = append(out.SuccessfulTasks, r.TaskID)
			out.ContributingAgents = append(out.ContributingAgents, r.AgentID)
			agentOutputs[r.AgentID] = r.OutputText
		case StatusFailed:
			out.FailedTasks = append(out.FailedTasks, r.TaskID)
		case StatusCancelled, StatusTimedOut:
			out.CancelledTasks = append(out.CancelledTasks, r.TaskID)
		}
	}

	out.StructuredData["agent_outputs"] = agentOutputs
	out.StructuredData["task_count"] = len(results)
	out.StructuredData["successful_count"] = len(successful)

	// Check for simple detectable conflicts between outputs
	ag.detectConflicts(successful, &out)

	if len(successful) == 0 {
		out.FinalText = "No agent tasks completed successfully."
		return out, nil
	}

	if policy == "" {
		policy = AggregationCollectAll
	}

	switch policy {
	case AggregationPrimary:
		// Find primary agent result
		var chosen *TaskExecutionResult
		for _, r := range successful {
			if r.AgentID == primaryAgentID {
				chosen = r
				break
			}
		}
		if chosen == nil && len(successful) > 0 {
			chosen = successful[0] // fallback to first successful
		}
		if chosen != nil {
			out.FinalText = chosen.OutputText
			out.StructuredData["primary_agent"] = chosen.AgentID
		}

	case AggregationOrdered:
		var sb strings.Builder
		for i, r := range successful {
			if i > 0 {
				sb.WriteString("\n\n---\n\n")
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s", r.AgentID, r.OutputText))
		}
		out.FinalText = sb.String()

	case AggregationMerge:
		var sb strings.Builder
		sb.WriteString("### Merged Multi-Agent Result:\n\n")
		for _, r := range successful {
			sb.WriteString(fmt.Sprintf("#### Output from %s:\n%s\n\n", r.AgentID, r.OutputText))
		}
		out.FinalText = strings.TrimSpace(sb.String())

	case AggregationSynthesizer:
		if synthesizerAgent != nil && ag.runtime != nil {
			synthInput := "Please synthesize the following outputs from team agents into a coherent final result:\n\n"
			for _, r := range successful {
				synthInput += fmt.Sprintf("Agent %s output:\n%s\n\n", r.AgentID, r.OutputText)
			}
			synthTask := TaskRecord{
				ID:        "synth-" + fmt.Sprintf("%d", time.Now().UnixNano()),
				AgentID:   synthesizerAgent.ID,
				InputData: synthInput,
			}
			synthRes, err := ag.runtime.ExecuteTask(ctx, *synthesizerAgent, synthTask, ContextInput{})
			if err == nil && synthRes != nil && synthRes.Status == StatusCompleted {
				out.FinalText = synthRes.OutputText
				out.ContributingAgents = append(out.ContributingAgents, synthesizerAgent.ID)
				out.StructuredData["synthesizer_agent"] = synthesizerAgent.ID
				return out, nil
			}
			// Fallback to collect all if synthesis fails
			out.ConflictDetails = append(out.ConflictDetails, "Synthesizer agent execution failed, falling back to collect all")
		}
		fallthrough

	case AggregationCollectAll:
		fallthrough
	default:
		var sb strings.Builder
		for i, r := range successful {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s", r.AgentID, r.OutputText))
		}
		out.FinalText = sb.String()
	}

	return out, nil
}

func (ag *Aggregator) detectConflicts(results []*TaskExecutionResult, out *AggregatedResult) {
	if len(results) < 2 {
		return
	}
	// Check for obvious binary contradictions
	positives := 0
	negatives := 0
	for _, r := range results {
		lower := strings.ToLower(r.OutputText)
		if strings.Contains(lower, "yes") || strings.Contains(lower, "approve") || strings.Contains(lower, "success") {
			positives++
		}
		if strings.Contains(lower, "no") || strings.Contains(lower, "reject") || strings.Contains(lower, "fail") {
			negatives++
		}
	}
	if positives > 0 && negatives > 0 {
		out.HasConflicts = true
		out.ConflictDetails = append(out.ConflictDetails, fmt.Sprintf("Divergent sentiment detected across agents: %d affirmative vs %d negative", positives, negatives))
	}
}
