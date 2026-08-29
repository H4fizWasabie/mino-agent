package main

// Agent personas — per-playbook prompt profiles (PSN-001). Each persona is a
// "hat" the same brain wears for a playbook run: ~0.9–1.1KB of stance,
// mission, lens, and deliverable voice, bound deterministically from the
// playbook's config.md (`agent: <name>`) — never fuzzy-matched like skills.
// Workspace personas are authoritative; the shared roster is a migration
// fallback for legacy playbooks and stays separate from skills.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxPersonaBodyBytes caps the persona body at validation time with an
// explicit error — never truncated (silent truncation changes persona
// behavior the author didn't see). The cap is the cost-control mechanism
// under the low-input-tokens-primary priority: the body rides in every
// system prompt of every run wearing the hat.
const maxPersonaBodyBytes = 2048

// Agent is one playbook persona. Description is dashboard display only —
// binding is deterministic, so a missing description never blocks a run.
type Agent struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	// Tools is deliberately unsupported: the stage contract declares tools;
	// a second tool-authorization source is the one thing the architecture
	// explicitly avoids.
	Tools []string `yaml:"tools"`
	Body  string   `yaml:"-" json:"-"`
}

// parsePersonaFile mirrors parseSkillFile's shape (frontmatter regex + yaml)
// but is its own parser: a silent reuse of the skill parser would inherit
// triggers/usage-tracking machinery personas don't need.
func parsePersonaFile(path string) (*Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	match := frontmatterRe.FindStringSubmatch(string(data))
	if match == nil {
		return nil, fmt.Errorf("no frontmatter")
	}
	var a Agent
	if err := yaml.Unmarshal([]byte(match[1]), &a); err != nil || a.Name == "" {
		return nil, fmt.Errorf("invalid frontmatter")
	}
	if len(a.Tools) > 0 {
		return nil, fmt.Errorf("persona files must not declare tools — the stage contract is the only tool authorization")
	}
	a.Body = strings.TrimSpace(match[2])
	return &a, nil
}

// loadPersonaFile resolves a config.md `agent:` binding to one persona file.
// The frontmatter name must match the binding exactly (case/space mismatch
// would otherwise be a silent miss).
func loadPersonaFile(path, name string) (*Agent, error) {
	if !validPlaybookName(name) {
		return nil, fmt.Errorf("invalid persona name %q (use lowercase letters, digits, and single hyphens)", name)
	}
	agent, err := parsePersonaFile(path)
	if err != nil {
		return nil, fmt.Errorf("persona %q unavailable: %v", name, err)
	}
	if agent.Name != name {
		return nil, fmt.Errorf("persona file %s declares name %q — it must match the binding %q exactly", name+".md", agent.Name, name)
	}
	return agent, nil
}

// loadAgentPersona resolves a legacy config.md binding to the shared roster.
func loadAgentPersona(home, name string) (*Agent, error) {
	return loadPersonaFile(filepath.Join(home, "agents", name+".md"), name)
}

// loadPlaybookPersona resolves workspace-owned personas first. Only a legacy
// playbook without AGENTS.md may fall back to the shared roster; migrated
// workspaces must declare their persona beside their routing files.
func loadPlaybookPersona(home string, pb *PlaybookWorkspace) (*Agent, error) {
	if pb == nil || pb.Agent == "" {
		return nil, nil
	}
	if !validPlaybookName(pb.Agent) {
		return nil, fmt.Errorf("invalid persona name %q (use lowercase letters, digits, and single hyphens)", pb.Agent)
	}
	if pb.Dir != "" {
		path := filepath.Join(pb.Dir, "persona", pb.Agent+".md")
		if _, err := os.Stat(path); err == nil {
			return loadPersonaFile(path, pb.Agent)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("persona %q unavailable: %v", pb.Agent, err)
		}
		if pb.agentsPresent {
			return nil, fmt.Errorf("persona %q unavailable: workspace persona/%s.md is required", pb.Agent, pb.Agent)
		}
	}
	return loadAgentPersona(home, pb.Agent)
}

// validatePlaybookPersona resolves a playbook's `agent:` reference against
// the roster, mirroring validateWorkspaceStageTools's unknown-tool rejection:
// a missing persona refuses the playbook at edit time and pre-run.
func validatePlaybookPersona(home string, pb *PlaybookWorkspace) error {
	if pb.Agent == "" {
		return nil
	}
	agent, err := loadPlaybookPersona(home, pb)
	if err != nil {
		return fmt.Errorf("playbook %s: %v", pb.Name, err)
	}
	if len(agent.Body) > maxPersonaBodyBytes {
		return fmt.Errorf("playbook %s: agent %q persona body is %d bytes, over the %d-byte cap — shorten the persona", pb.Name, pb.Agent, len(agent.Body), maxPersonaBodyBytes)
	}
	return nil
}
