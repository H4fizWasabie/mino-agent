package main

// Mino — runtime/session.py — Core's exact session pattern.
// Working memory = SOUL.md + gated memory + chat history + user message.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultSoul = `You are Mino, a personal AI assistant.
You are concise, warm, and proactive. Answer briefly.

TOOL DISCIPLINE (STRICT):
- Call each tool exactly ONCE per turn. Never re-run the same tool with the same args.
  If you see "[already executed]" in a tool result, you called it twice. Move on.
- Check your response BEFORE sending: did you already use this tool this turn? If yes, skip it.
- When in doubt, REPLY to the user instead of calling more tools.
- Tool results are final. Do not re-interpret, re-query, or second-guess them.

SELF-VERIFY BEFORE REPLYING:
- Before sending your final reply, silently ask: "What did the user ask me to DO?"
- If the answer involves creating, scheduling, saving, deleting, or modifying anything:
  did you actually CALL the tool and receive a success response?
- If NO → call the tool NOW. Do not reply until you have the tool result.
- Saying "Done!" in a text reply does NOT count as done. Only a tool_use block counts.

STOP CONDITIONS:
- You're done when you have enough info to answer the user.
- No tool calls needed? Reply directly. Don't search for tools to use.
- After 3 tool calls, STOP and synthesize your answer. Do not keep digging.

MEMORY:
- When asked about past conversations, facts, or user preferences, call recall FIRST.
- When the user tells you something worth remembering, call save_note.

IDENTITY: your name is Mino. You are a personal AI assistant running on a VPS.
`

type Session struct {
	settings  *Settings
	mem       *Memory
	sessionID string
	history   []Message
}

func NewSession(s *Settings, mem *Memory) *Session {
	return &Session{settings: s, mem: mem, sessionID: "default", history: make([]Message, 0)}
}

// loadSoul — Core's load_soul(): editable persona file.
func loadSoul(home string) string {
	path := filepath.Join(home, "SOUL.md")
	if _, err := os.Stat(path); err != nil {
		os.WriteFile(path, []byte(defaultSoul), 0644)
	}
	data, _ := os.ReadFile(path)
	return string(data)
}

// BuildSystem — Core's build_system():
//
//	SOUL.md + current time + pending approvals + relevant skill matches. Memory is pulled via recall.
func (s *Session) BuildSystem(userMessage string) string {
	now := time.Now().Format("Monday, 2006-01-02 15:04 MST")
	parts := []string{
		loadSoul(s.settings.Home),
		fmt.Sprintf("\nRight now it is %s.", now),
	}

	// inject pending approvals so the user sees them in any conversation
	if s.settings != nil {
		pendingDir := filepath.Join(s.settings.Home, "pending")
		if entries, err := os.ReadDir(pendingDir); err == nil && len(entries) > 0 {
			var pending []string
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				data, _ := os.ReadFile(filepath.Join(pendingDir, e.Name()))
				var req map[string]any
				if json.Unmarshal(data, &req) == nil {
					title, _ := req["title"].(string)
					actionID := strings.TrimSuffix(e.Name(), ".json")
					pending = append(pending, fmt.Sprintf("- [%s] %s", actionID, title))
				}
			}
			if len(pending) > 0 {
				parts = append(parts, "\n\u23f3 PENDING APPROVALS (use resolve_approval to approve/reject):\n"+strings.Join(pending, "\n"))
			}
		}
	}

	if s.mem != nil {
		skills := s.mem.MatchingSkills(userMessage)
		if skills != "" {
			parts = append(parts, "\nRelevant skill instructions:\n"+skills)
		}
	}
	return strings.Join(parts, "\n")
}

// AddExchange — Core's add_exchange(): folds tool activity into [tools used: ...]
func (s *Session) AddExchange(userRaw, userContext, reply string, toolCalls []ToolCall, source string) {
	record := reply
	if len(toolCalls) > 0 {
		parts := make([]string, 0)
		for _, tc := range toolCalls {
			parts = append(parts, fmt.Sprintf("%s(%v) -> %s", tc.Name, tc.Args, tc.Output))
		}
		record = fmt.Sprintf("%s\n[tools used: %s]", reply, strings.Join(parts, "; "))
	}
	s.history = append(s.history,
		Message{Role: "user", Content: userContext},
		Message{Role: "assistant", Content: record},
	)
	if s.mem != nil {
		s.mem.LogChat("user", userRaw, s.sessionID, source)
		s.mem.LogChat("assistant", record, s.sessionID, source)
		for _, tc := range toolCalls {
			if artifact, ok := artifactFromOutput(tc.Output); ok {
				s.mem.RecordArtifact(s.sessionID, artifact.Label, artifact.Path, artifact.Size)
			}
		}
	}
}

func (s *Session) ContextMessages(maxChars int) []Message {
	history := make([]Message, len(s.history))
	for i, message := range s.history {
		history[i] = message
		if len(message.Content) > inputPreviewLimit {
			history[i].Content = fmt.Sprintf("[Large previous %s message (%d chars) is retained in the session artifact catalog.]", message.Role, len(message.Content))
		}
	}
	if maxChars <= 0 {
		return history
	}
	if len(history) <= 2 {
		return history
	}
	total := 0
	for _, message := range history {
		total += len(message.Content)
	}
	if total <= maxChars {
		return history
	}
	marker := "[Earlier conversation is retained but compacted. Use recall when details matter.]"
	used := len(history[0].Content) + len(history[1].Content)
	start := len(history)
	for start-2 >= 2 {
		pair := len(history[start-2].Content) + len(history[start-1].Content)
		if used+len(marker)+pair > maxChars {
			break
		}
		start -= 2
		used += pair
	}
	out := append([]Message{}, history[:2]...)
	out = append(out, Message{Role: "assistant", Content: marker})
	out = append(out, history[start:]...)
	return out
}

func (s *Session) ContextFor(system, userMessage string) ([]Message, string) {
	catalog := ""
	if s.mem != nil {
		catalog = s.mem.SessionArtifacts(s.sessionID, 2000)
	}
	available := s.settings.ContextChars - len(system) - len(catalog)
	preview := min(inputPreviewLimit, max(512, available/4))
	userContext, artifact := compactUserInput(s.sessionID, userMessage, preview)
	if s.mem != nil && artifact.Path != "" {
		s.mem.RecordArtifact(s.sessionID, artifact.Label, artifact.Path, artifact.Size)
	}
	historyBudget := max(0, s.settings.ContextChars-len(system)-len(catalog)-len(userContext))
	messages := s.ContextMessages(historyBudget)
	if catalog != "" {
		messages = append(messages, Message{Role: "assistant", Content: catalog})
	}
	messages = append(messages, Message{Role: "user", Content: userContext})
	return messages, userContext
}

func (s *Session) StartNew(id string) {
	s.sessionID = id
	s.history = nil
}

func (s *Session) Switch(id string) {
	s.sessionID = id
	s.history = nil
	if s.mem != nil {
		for _, pair := range s.mem.SessionHistory(id) {
			s.history = append(s.history,
				Message{Role: "user", Content: pair[0]},
				Message{Role: "assistant", Content: pair[1]},
			)
		}
	}
}

// ToolCall records a tool execution result for add_exchange.
type ToolCall struct {
	Name   string
	Args   map[string]any
	Output string
}
