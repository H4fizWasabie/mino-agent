package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryAuditReport is a read-only inventory of live Markdown graph facts.
// Parse failures are reported rather than silently omitted.
type MemoryAuditReport struct {
	Dir                 string
	Files               int
	Parsed              int
	ParseFailures       []memoryAuditEntry
	SourceLess          []memoryAuditEntry
	DuplicateIDs        [][]memoryAuditEntry
	ExactDuplicates     [][]memoryAuditEntry
	ConflictingSubjects [][]memoryAuditEntry
	StaleSnapshots      []memoryAuditEntry
}

type memoryAuditEntry struct {
	File    string
	ID      string
	Type    string
	Tier    string
	At      time.Time
	Source  string
	Origin  string
	Subject string
	Body    string
	Reason  string
}

// AuditMemoryDir scans only the top-level live fact files. It does not create
// an index, refresh a graph, call a provider, or write to the directory.
func AuditMemoryDir(dir string) (MemoryAuditReport, error) {
	report := MemoryAuditReport{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report, err
	}

	parser := &GraphMemory{}
	now := time.Now()
	byID := make(map[string][]memoryAuditEntry)
	bySubject := make(map[string][]memoryAuditEntry)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		report.Files++
		path := filepath.Join(dir, entry.Name())
		fact, err := parser.readFile(path)
		if err != nil {
			report.ParseFailures = append(report.ParseFailures, memoryAuditEntry{File: entry.Name(), Reason: err.Error()})
			continue
		}
		report.Parsed++
		item := auditEntry(entry.Name(), *fact)
		if strings.TrimSpace(fact.Source) == "" {
			report.SourceLess = append(report.SourceLess, item)
		}
		if fact.ID != "" {
			byID[fact.ID] = append(byID[fact.ID], item)
		}
		if key := normalizeAuditText(fact.Subject); key != "" {
			bySubject[key] = append(bySubject[key], item)
		}
		if staleSnapshot(*fact, now) {
			report.StaleSnapshots = append(report.StaleSnapshots, item)
		}
	}

	for _, group := range byID {
		if len(group) > 1 {
			report.DuplicateIDs = append(report.DuplicateIDs, sortedAuditEntries(group))
		}
	}
	for _, group := range bySubject {
		if len(group) < 2 {
			continue
		}
		bodies := make(map[string]bool)
		for _, item := range group {
			bodies[normalizeAuditText(item.Body)] = true
		}
		group = sortedAuditEntries(group)
		if len(bodies) == 1 {
			report.ExactDuplicates = append(report.ExactDuplicates, group)
		} else {
			report.ConflictingSubjects = append(report.ConflictingSubjects, group)
		}
	}

	sortAuditGroups(report.DuplicateIDs)
	sortAuditGroups(report.ExactDuplicates)
	sortAuditGroups(report.ConflictingSubjects)
	sort.Slice(report.ParseFailures, func(i, j int) bool { return report.ParseFailures[i].File < report.ParseFailures[j].File })
	sort.Slice(report.SourceLess, func(i, j int) bool { return report.SourceLess[i].ID < report.SourceLess[j].ID })
	sort.Slice(report.StaleSnapshots, func(i, j int) bool { return report.StaleSnapshots[i].ID < report.StaleSnapshots[j].ID })
	return report, nil
}

func auditEntry(file string, fact Fact) memoryAuditEntry {
	return memoryAuditEntry{
		File: file, ID: fact.ID, Type: fact.Type, Tier: fact.Tier,
		At: fact.At, Source: fact.Source, Origin: auditOrigin(fact),
		Subject: fact.Subject, Body: fact.Body,
	}
}

func auditOrigin(fact Fact) string {
	if fact.Source != "" {
		return fact.Source
	}
	id := strings.ToLower(fact.ID)
	switch {
	case fact.Tier == "run" || strings.HasPrefix(id, "run_"):
		return "likely playbook run"
	case fact.Type == "episodic" || strings.HasPrefix(id, "ep_"):
		return "likely episodic/consolidation"
	case strings.Contains(id, "migrat"):
		return "likely legacy migration"
	case strings.Contains(id, "consolid"):
		return "likely consolidation"
	default:
		return "unknown"
	}
}

func staleSnapshot(fact Fact, now time.Time) bool {
	text := strings.ToLower(fact.Subject + " " + fact.Body)
	markers := []string{
		"current ", "currently", "configured", "configuration", "config", "provider",
		"model", "host", "port", "endpoint", "url", "schedule", "systemd",
		"runs on", "running on", "deployed on",
	}
	volatile := false
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			volatile = true
			break
		}
	}
	if !volatile {
		return false
	}
	if !fact.StaleAfter.IsZero() {
		return now.After(fact.StaleAfter)
	}
	return !fact.At.IsZero() && now.Sub(fact.At) >= 24*time.Hour
}

func normalizeAuditText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func sortedAuditEntries(entries []memoryAuditEntry) []memoryAuditEntry {
	out := append([]memoryAuditEntry(nil), entries...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].File < out[j].File
	})
	return out
}

func sortAuditGroups(groups [][]memoryAuditEntry) {
	sort.Slice(groups, func(i, j int) bool {
		return groups[i][0].ID+groups[i][0].File < groups[j][0].ID+groups[j][0].File
	})
}

// Format renders a bounded, deterministic report suitable for stdout or a
// redirected file. It describes repair paths but performs none of them.
func (r MemoryAuditReport) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Memory audit: %s\nScanned: %d Markdown files; parsed: %d; parse failures: %d\n\n", r.Dir, r.Files, r.Parsed, len(r.ParseFailures))
	formatAuditEntries(&b, "Source-less facts", len(r.SourceLess), r.SourceLess, "review provenance; use save_note or an explicit correction to create a provenance-bearing replacement")
	formatAuditGroups(&b, "Duplicate ID families", len(r.DuplicateIDs), r.DuplicateIDs, "manual ID/file repair; the normal graph loader cannot safely choose a winner")
	formatAuditGroups(&b, "Exact duplicate families", len(r.ExactDuplicates), r.ExactDuplicates, "review and merge; manage_memory consolidate is the existing consolidation path")
	formatAuditGroups(&b, "Same-subject/different-body families", len(r.ConflictingSubjects), r.ConflictingSubjects, "manual review; use an explicit correction to supersede a fact only after confirming the truth")
	formatAuditEntries(&b, "Stale config-snapshot candidates", len(r.StaleSnapshots), r.StaleSnapshots, "verify against the live config/tool, then correct or archive through the existing memory maintenance path")
	if len(r.ParseFailures) > 0 {
		formatAuditEntries(&b, "Unreadable fact files", len(r.ParseFailures), r.ParseFailures, "repair the Markdown front matter before any consolidation or migration")
	}
	return b.String()
}

const auditExampleLimit = 10

func formatAuditEntries(b *strings.Builder, title string, count int, entries []memoryAuditEntry, repair string) {
	fmt.Fprintf(b, "%s: %d\n", title, count)
	for i, item := range entries {
		if i == auditExampleLimit {
			fmt.Fprintf(b, "- ... %d more\n", count-i)
			break
		}
		fmt.Fprintf(b, "- %s (%s) file=%s subject=%q", item.ID, item.Origin, item.File, oneLine(item.Subject, 120))
		if item.Reason != "" {
			fmt.Fprintf(b, " reason=%s", item.Reason)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "  Repair path: %s\n\n", repair)
}

func formatAuditGroups(b *strings.Builder, title string, count int, groups [][]memoryAuditEntry, repair string) {
	fmt.Fprintf(b, "%s: %d\n", title, count)
	for i, group := range groups {
		if i == auditExampleLimit {
			fmt.Fprintf(b, "- ... %d more\n", count-i)
			break
		}
		b.WriteString("- family:\n")
		for _, item := range group {
			fmt.Fprintf(b, "  - %s file=%s subject=%q body=%q\n", item.ID, item.File, oneLine(item.Subject, 100), oneLine(item.Body, 140))
		}
	}
	fmt.Fprintf(b, "  Repair path: %s\n\n", repair)
}

func AuditMemory(s *Settings) error {
	report, err := AuditMemoryDir(s.MemoriesDir)
	if err != nil {
		return err
	}
	fmt.Print(report.Format())
	return nil
}
