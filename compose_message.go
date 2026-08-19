package main

import (
	"context"
	"strings"
)

// compose_message — SCR-002 (#277): LLM-synthesized Telegram reports for
// script-backed playbooks. A script passes its verified digest; ONE bounded
// single-turn provider call (no tools array, no iteration loop, no
// serialization) returns the message. The degeneration class is
// structurally impossible: text in → text out, no tool access. Scripts
// call it via `mino exec`; the call lands in tool_calls + audit like any
// tool. Secrets stay in the binary — the digest is the only input.

// composeSystemPrompt is a named prompt-assembly seam (REL-04): the fixed
// synthesis contract. Numbers and facts come only from the digest — the
// verification discipline rides into the message layer.
const composeSystemPrompt = `You are Mino's Telegram message composer. Write ONE concise report message from the digest provided. Rules: every number and fact comes ONLY from the digest — never fabricate, never infer beyond it; Mino's voice: brief, direct, factual, acerbic when warranted; plain text, no markdown tables; at most ~150 words; exactly one message. Output ONLY the final message text: no reasoning, no preamble, no commentary, no quotes around it.`

func makeComposeMessageTool(client LLMClient) *Tool {
	t := &Tool{
		Name:        "compose_message",
		Description: "Synthesize a Telegram report message from a verified digest via one bounded single-turn LLM call. Script-backed playbooks use this for their notify leg (SCR-002).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"digest": map[string]any{"type": "string", "description": "Verified data to report — the only source of numbers and facts"},
			},
			"required": []string{"digest"},
		},
	}
	return contextualTool(t, func(ctx context.Context, args map[string]any) string {
		digest, _ := args["digest"].(string)
		if strings.TrimSpace(digest) == "" {
			return "Error: compose_message requires a non-empty digest"
		}
		if client == nil {
			return "Error: compose_message: provider client not available"
		}
		sid := "cli-exec"
		if v := ctx.Value(sessionIDKey{}); v != nil {
			if s, ok := v.(string); ok && s != "" {
				sid = s
			}
		}
		resp, err := client.CreateContext(ctx, sid, MainModel,
			[]Message{{Role: "user", Content: digest}}, 300, composeSystemPrompt, nil)
		if err != nil {
			return "Error: compose_message: " + err.Error()
		}
		msg := strings.TrimSpace(extractText(resp.Content))
		if msg == "" {
			return "Error: compose_message: empty provider response"
		}
		return msg
	})
}
