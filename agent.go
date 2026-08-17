package main

// Agent personas — per-playbook prompt profiles (PSN-001). Each persona is a
// "hat" the same brain wears for a playbook run: ~0.9–1.1KB of stance,
// mission, lens, and deliverable voice, bound deterministically from the
// playbook's config.md (`agent: <name>`) — never fuzzy-matched like skills.
// The roster lives in ~/.mino/agents/<name>.md, deliberately separate from
// skills/: no triggers, no usage tracking, deterministic binding only.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxPersonaBodyBytes caps the persona body at validation time with an
// explicit error — never truncated (silent truncation changes persona
// behavior the author didn't see). The cap is the cost-control mechanism
// under the low-input-tokens-primary priority: the body rides in every
// system prompt of every run wearing the hat.
const maxPersonaBodyBytes = 2048

// Agent is one roster persona. Description is dashboard display only —
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
	match := regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n(.*)$`).FindStringSubmatch(string(data))
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

// loadAgentPersona resolves a config.md `agent:` binding to its roster file.
// The frontmatter name must match the binding exactly (case/space mismatch
// would otherwise be a silent miss).
func loadAgentPersona(home, name string) (*Agent, error) {
	if !validPlaybookName(name) {
		return nil, fmt.Errorf("invalid persona name %q (use lowercase letters, digits, and single hyphens)", name)
	}
	agent, err := parsePersonaFile(filepath.Join(home, "agents", name+".md"))
	if err != nil {
		return nil, fmt.Errorf("persona %q unavailable: %v", name, err)
	}
	if agent.Name != name {
		return nil, fmt.Errorf("persona file %s declares name %q — it must match the binding %q exactly", name+".md", agent.Name, name)
	}
	return agent, nil
}

// validatePlaybookPersona resolves a playbook's `agent:` reference against
// the roster, mirroring validateWorkspaceStageTools's unknown-tool rejection:
// a missing persona refuses the playbook at edit time and pre-run.
func validatePlaybookPersona(home string, pb *PlaybookWorkspace) error {
	if pb.Agent == "" {
		return nil
	}
	agent, err := loadAgentPersona(home, pb.Agent)
	if err != nil {
		return fmt.Errorf("playbook %s: %v", pb.Name, err)
	}
	if len(agent.Body) > maxPersonaBodyBytes {
		return fmt.Errorf("playbook %s: agent %q persona body is %d bytes, over the %d-byte cap — shorten the persona", pb.Name, pb.Agent, len(agent.Body), maxPersonaBodyBytes)
	}
	return nil
}
