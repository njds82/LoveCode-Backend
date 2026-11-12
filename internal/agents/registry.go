package agents

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Registry struct {
	store *Store
	mu    sync.RWMutex

	agents        map[string]AgentDefinition
	teams         map[string]TeamDefinition
	dynamicAgents map[string]map[string]AgentDefinition // runID -> map[agentID]AgentDefinition
}

func NewRegistry(store *Store) *Registry {
	return &Registry{
		store:         store,
		agents:        make(map[string]AgentDefinition),
		teams:         make(map[string]TeamDefinition),
		dynamicAgents: make(map[string]map[string]AgentDefinition),
	}
}

// LoadFromStore loads all persisted agent and team definitions from the database into memory.
func (r *Registry) LoadFromStore(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agents, err := r.store.ListAgents(ctx, true)
	if err != nil {
		return fmt.Errorf("load agents: %w", err)
	}
	r.agents = make(map[string]AgentDefinition, len(agents))
	for _, a := range agents {
		r.agents[a.ID] = a
	}

	teams, err := r.store.ListTeams(ctx, true)
	if err != nil {
		return fmt.Errorf("load teams: %w", err)
	}
	r.teams = make(map[string]TeamDefinition, len(teams))
	for _, t := range teams {
		r.teams[t.ID] = t
	}

	return nil
}

// RegisterAgent persists and registers an agent definition.
func (r *Registry) RegisterAgent(ctx context.Context, a AgentDefinition) error {
	if err := a.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = time.Now().UTC()

	if err := r.store.SaveAgent(ctx, a); err != nil {
		return err
	}
	r.agents[a.ID] = a
	return nil
}

// GetAgent retrieves an agent definition by ID (checking run-scoped dynamic agents first, then registry).
func (r *Registry) GetAgent(runID, id string) (AgentDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if runID != "" {
		if runMap, ok := r.dynamicAgents[runID]; ok {
			if a, found := runMap[id]; found {
				return a, nil
			}
		}
	}

	if a, ok := r.agents[id]; ok {
		return a, nil
	}
	return AgentDefinition{}, ErrNotFound
}

// ListAgents returns all registered permanent agents.
func (r *Registry) ListAgents(includeDisabled bool) []AgentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []AgentDefinition
	for _, a := range r.agents {
		if !includeDisabled && !a.Enabled {
			continue
		}
		list = append(list, a)
	}
	return list
}

// DeleteAgent removes an agent from memory and persistent storage. Historical records are preserved.
func (r *Registry) DeleteAgent(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.agents[id]; !ok {
		return ErrNotFound
	}
	if err := r.store.DeleteAgent(ctx, id); err != nil {
		return err
	}
	delete(r.agents, id)
	return nil
}

// SetAgentEnabled enables or disables an agent.
func (r *Registry) SetAgentEnabled(ctx context.Context, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, ok := r.agents[id]
	if !ok {
		return ErrNotFound
	}
	a.Enabled = enabled
	a.UpdatedAt = time.Now().UTC()
	if err := r.store.SaveAgent(ctx, a); err != nil {
		return err
	}
	r.agents[id] = a
	return nil
}

// RegisterTeam persists and registers a team definition.
func (r *Registry) RegisterTeam(ctx context.Context, t TeamDefinition) error {
	if err := t.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()

	if err := r.store.SaveTeam(ctx, t); err != nil {
		return err
	}
	r.teams[t.ID] = t
	return nil
}

func (r *Registry) GetTeam(id string) (TeamDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if t, ok := r.teams[id]; ok {
		return t, nil
	}
	return TeamDefinition{}, ErrNotFound
}

func (r *Registry) ListTeams(includeDisabled bool) []TeamDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []TeamDefinition
	for _, t := range r.teams {
		if !includeDisabled && !t.Enabled {
			continue
		}
		list = append(list, t)
	}
	return list
}

func (r *Registry) DeleteTeam(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.teams[id]; !ok {
		return ErrNotFound
	}
	if err := r.store.DeleteTeam(ctx, id); err != nil {
		return err
	}
	delete(r.teams, id)
	return nil
}

func (r *Registry) SetTeamEnabled(ctx context.Context, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.teams[id]
	if !ok {
		return ErrNotFound
	}
	t.Enabled = enabled
	t.UpdatedAt = time.Now().UTC()
	if err := r.store.SaveTeam(ctx, t); err != nil {
		return err
	}
	r.teams[id] = t
	return nil
}

// Dynamic Agent Management & Privilege Inheritance Control

// RegisterDynamicAgent registers an ephemeral agent for a specific run, verifying that it does not exceed parent permissions.
func (r *Registry) RegisterDynamicAgent(runID string, parentAgentID string, dynamicAgent AgentDefinition, maxDynamicAgents int) error {
	if err := dynamicAgent.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	runMap, exists := r.dynamicAgents[runID]
	if !exists {
		runMap = make(map[string]AgentDefinition)
		r.dynamicAgents[runID] = runMap
	}

	if maxDynamicAgents > 0 && len(runMap) >= maxDynamicAgents {
		return fmt.Errorf("maximum dynamic agents limit reached for run (%d)", maxDynamicAgents)
	}

	// Verify permissions inheritance: child cannot have permissions parent lacks
	if parentAgentID != "" {
		parent, ok := r.agents[parentAgentID]
		if !ok {
			// check dynamic parent
			parent, ok = runMap[parentAgentID]
		}
		if ok {
			if err := verifyPermissionInheritance(parent.Permissions, dynamicAgent.Permissions); err != nil {
				return fmt.Errorf("privilege escalation rejected: %w", err)
			}
			if err := verifyPermissionInheritance(parent.Capabilities, dynamicAgent.Capabilities); err != nil {
				return fmt.Errorf("capability escalation rejected: %w", err)
			}
		}
	}

	dynamicAgent.IsDynamic = true
	dynamicAgent.Enabled = true
	dynamicAgent.CreatedAt = time.Now().UTC()
	dynamicAgent.UpdatedAt = time.Now().UTC()

	runMap[dynamicAgent.ID] = dynamicAgent
	return nil
}

// CleanupDynamicAgents removes ephemeral agent definitions created for a completed run.
func (r *Registry) CleanupDynamicAgents(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.dynamicAgents, runID)
}

func verifyPermissionInheritance(parentPerms, childPerms []string) error {
	if len(childPerms) == 0 {
		return nil
	}
	parentSet := make(map[string]bool, len(parentPerms))
	for _, p := range parentPerms {
		if p == "admin" || p == "*" {
			return nil // parent has full permissions
		}
		parentSet[p] = true
	}
	for _, cp := range childPerms {
		if !parentSet[cp] {
			return fmt.Errorf("child agent requests permission %q not granted to parent", cp)
		}
	}
	return nil
}
