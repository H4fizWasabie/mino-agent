package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string   `yaml:"name"        json:"name"`
	Description string   `yaml:"description" json:"description"`
	Triggers    []string `yaml:"triggers"    json:"triggers"`
	Body        string   `yaml:"-"           json:"-"`
	Source      string   `yaml:"-"           json:"source"`
	UseCount    int      `json:"use_count"`
	LastUsedAt  float64  `json:"last_used_at"`
	State       string   `json:"state"`
	CreatedAt   float64  `json:"created_at"`
}

type SkillLoader struct {
	dir       string
	embedder  *EmbeddingStore
	skills    map[string]*Skill
	lastScan  time.Time
	usagePath string
	mu        sync.Mutex
}

const maxSkillMatches = 3

func NewSkillLoader(home string, embedder *EmbeddingStore) *SkillLoader {
	sl := &SkillLoader{
		dir:       filepath.Join(home, "skills"),
		embedder:  embedder,
		skills:    map[string]*Skill{},
		usagePath: filepath.Join(home, "skills_usage.json"),
	}
	sl.refresh()
	sl.loadUsage()
	return sl
}

func (sl *SkillLoader) Match(message string) []*Skill {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.stale() {
		sl.refresh()
	}
	lowered := strings.ToLower(message)
	var hits []*Skill
	for _, s := range sl.skills {
		if s.State != "active" && s.State != "pinned" {
			continue
		}
		for _, t := range s.Triggers {
			if strings.Contains(lowered, strings.ToLower(t)) {
				hits = append(hits, s)
				goto next
			}
		}
		if s.Description != "" && strings.Contains(lowered, strings.ToLower(s.Description)) {
			hits = append(hits, s)
			goto next
		}
		if skillWords(s) != nil && inputWords(lowered) != nil {
			skillSet := skillWords(s)
			inputSet := inputWords(lowered)
			if len(skillSet) >= 2 && intersectCount(skillSet, inputSet) >= 2 {
				hits = append(hits, s)
			} else if len(skillSet) == 1 && intersectCount(skillSet, inputSet) >= 1 {
				hits = append(hits, s)
			}
		}
	next:
	}
	if len(hits) == 0 && sl.embedder != nil {
		hits = sl.semanticMatch(message)
	}
	if len(hits) > maxSkillMatches {
		hits = hits[:maxSkillMatches]
	}
	now := float64(time.Now().Unix())
	for _, s := range hits {
		s.UseCount++
		s.LastUsedAt = now
	}
	if len(hits) > 0 {
		sl.saveUsage()
	}
	return hits
}

type skCandidate struct {
	skill *Skill
	score float64
}

func (sl *SkillLoader) semanticMatch(message string) []*Skill {
	qEmb, err := sl.embedder.Embed(message)
	if err != nil || len(qEmb) == 0 {
		return nil
	}
	var candidates []skCandidate
	for _, s := range sl.skills {
		if s.State != "active" && s.State != "pinned" {
			continue
		}
		text := s.Name + ": " + s.Description
		if len(s.Triggers) > 0 {
			text += ". Triggers: " + strings.Join(s.Triggers, ", ")
		}
		sEmb, err := sl.embedder.Embed(text)
		if err != nil {
			continue
		}
		candidates = append(candidates, skCandidate{s, cosineSimilarity(qEmb, sEmb)})
	}
	sortSkCandidates(candidates)
	var hits []*Skill
	for _, c := range candidates {
		if c.score >= 0.5 && len(hits) < maxSkillMatches {
			hits = append(hits, c.skill)
		}
	}
	return hits
}

func sortSkCandidates(c []skCandidate) {
	sort.Slice(c, func(i, j int) bool { return c[i].score > c[j].score })
}

func (sl *SkillLoader) Bodies(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var out strings.Builder
	for _, s := range skills {
		out.WriteString(fmt.Sprintf("### %s\n%s\n\n", s.Name, s.Body))
	}
	return out.String()
}

func (sl *SkillLoader) Catalog() []*Skill {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.stale() {
		sl.refresh()
	}
	list := make([]*Skill, 0, len(sl.skills))
	for _, s := range sl.skills {
		list = append(list, s)
	}
	return list
}

func (sl *SkillLoader) Create(name, description string, triggers []string, body string) error {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	dir := filepath.Join(sl.dir, slug)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	fm := map[string]any{
		"name":        slug,
		"description": description,
		"triggers":    triggers,
	}
	y, _ := yaml.Marshal(fm)
	content := fmt.Sprintf("---\n%s---\n\n%s\n", y, body)
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return err
	}
	sl.refresh()
	return nil
}

func (sl *SkillLoader) MarkStale(name string) {
	if s, ok := sl.skills[name]; ok && s.State == "active" {
		s.State = "stale"
		sl.saveUsage()
	}
}

func (sl *SkillLoader) Pin(name string) {
	if s, ok := sl.skills[name]; ok {
		s.State = "pinned"
		sl.saveUsage()
	}
}

func (sl *SkillLoader) AutoStale(days int) {
	if days <= 0 {
		return
	}
	cutoff := float64(time.Now().AddDate(0, 0, -days).Unix())
	for _, s := range sl.skills {
		if s.State == "active" && s.UseCount == 0 && s.CreatedAt < cutoff {
			s.State = "stale"
		}
	}
	sl.saveUsage()
}

var (
	skillWordCache = map[string]map[string]bool{}
	_skillWordRE   = regexp.MustCompile(`[a-z0-9]{3,}`)
)

func skillWords(s *Skill) map[string]bool {
	key := s.Name
	if w, ok := skillWordCache[key]; ok {
		return w
	}
	text := strings.ToLower(s.Name + " " + s.Description)
	for _, t := range s.Triggers {
		text += " " + strings.ToLower(t)
	}
	words := _skillWordRE.FindAllString(text, -1)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	skillWordCache[key] = set
	return set
}

func inputWords(msg string) map[string]bool {
	words := _skillWordRE.FindAllString(msg, -1)
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

func intersectCount(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

func (sl *SkillLoader) refresh() {
	sl.skills = map[string]*Skill{}
	if _, err := os.Stat(sl.dir); os.IsNotExist(err) {
		sl.lastScan = time.Now()
		return
	}
	filepath.Walk(sl.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Name() != "SKILL.md" {
			return nil
		}
		skill, err := parseSkillFile(path)
		if err != nil || skill == nil {
			return nil
		}
		skill.Source = path
		skill.CreatedAt = float64(info.ModTime().Unix())
		if old, ok := sl.skills[skill.Name]; ok {
			skill.UseCount = old.UseCount
			skill.LastUsedAt = old.LastUsedAt
			skill.State = old.State
		}
		if skill.State == "" {
			skill.State = "active"
		}
		sl.skills[skill.Name] = skill
		return nil
	})
	skillWordCache = map[string]map[string]bool{}
	sl.lastScan = time.Now()
}

func (sl *SkillLoader) stale() bool {
	info, err := os.Stat(sl.dir)
	if err != nil {
		return false
	}
	return info.ModTime().After(sl.lastScan)
}

func parseSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	match := regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n(.*)$`).FindStringSubmatch(string(data))
	if match == nil {
		return nil, fmt.Errorf("no frontmatter")
	}
	var s Skill
	if err := yaml.Unmarshal([]byte(match[1]), &s); err != nil || s.Name == "" {
		return nil, fmt.Errorf("invalid frontmatter")
	}
	s.Body = strings.TrimSpace(match[2])
	return &s, nil
}

type usageEntry struct {
	UseCount   int     `json:"use_count"`
	LastUsedAt float64 `json:"last_used_at"`
	State      string  `json:"state"`
}

func (sl *SkillLoader) loadUsage() {
	data, err := os.ReadFile(sl.usagePath)
	if err != nil {
		return
	}
	var records map[string]*usageEntry
	if json.Unmarshal(data, &records) != nil {
		return
	}
	for name, u := range records {
		if s, ok := sl.skills[name]; ok {
			s.UseCount = u.UseCount
			s.LastUsedAt = u.LastUsedAt
			s.State = u.State
		}
	}
}

func (sl *SkillLoader) saveUsage() {
	records := map[string]*usageEntry{}
	for name, s := range sl.skills {
		if s.UseCount > 0 || s.State != "active" {
			records[name] = &usageEntry{
				UseCount:   s.UseCount,
				LastUsedAt: s.LastUsedAt,
				State:      s.State,
			}
		}
	}
	data, _ := json.Marshal(records)
	os.WriteFile(sl.usagePath, data, 0600)
}
