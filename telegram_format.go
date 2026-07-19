package main

// Markdown → Telegram HTML, ported from Crow's telegram_rich.py.
// Telegram HTML mode supports <b> <i> <u> <s> <code> <pre> <a>
// <blockquote> <tg-spoiler> — no tables or lists, so pipe tables
// render as aligned <pre> and list items as • lines.

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var (
	reFence   = regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
	reHeading = regexp.MustCompile(`^#{1,3}\s+(.+)$`)
	reBullet  = regexp.MustCompile(`^[-*]\s+(.+)$`)
	reInline  = regexp.MustCompile("`([^`\n]+)`")
	reLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBold    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reStrike  = regexp.MustCompile(`~~(.+?)~~`)
	reDivider = regexp.MustCompile(`^\|[\s\-:|]+\|$`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
)

const stashMark = "\x00STASH%d\x00"

// sendTelegramReply is the single exit point for outbound Telegram text:
// format → chunk → send. A chunk that Telegram rejects (malformed HTML →
// 400) is resent as plain text — stray tags beat a lost message.
func sendTelegramReply(bot *tgbotapi.BotAPI, chatID int64, reply string, tools []ToolCall) {
	html := formatTelegramHTML(reply, tools)
	for _, chunk := range chunkHTML(html, 4000) {
		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = tgbotapi.ModeHTML
		if _, err := bot.Send(msg); err != nil {
			slog.Warn("telegram html send failed, retrying plain", "error", err)
			if _, err := bot.Send(tgbotapi.NewMessage(chatID, chunk)); err != nil {
				slog.Warn("telegram plain send failed", "error", err)
			}
		}
	}
}

// formatTelegramHTML converts markdown-ish LLM output to Telegram HTML.
// Order is load-bearing: stash fences → escape → line pass → stash inline
// code → links/bold/strike → restore stashes.
func formatTelegramHTML(reply string, tools []ToolCall) string {
	var stash []string
	put := func(rendered string) string {
		stash = append(stash, rendered)
		return fmt.Sprintf(stashMark, len(stash)-1)
	}

	// 1. Fenced code blocks out first — protected from escape and inline rules.
	text := reFence.ReplaceAllStringFunc(reply, func(m string) string {
		p := reFence.FindStringSubmatch(m)
		if lang := p[1]; lang != "" {
			return put(`<pre><code class="language-` + lang + `">` + escapeHTML(p[2]) + "</code></pre>")
		}
		return put("<pre><code>" + escapeHTML(p[2]) + "</code></pre>")
	})

	// 2. Escape everything else.
	text = escapeHTML(text)

	// 3. Line pass: headings → <b>, list items → •.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if m := reHeading.FindStringSubmatch(s); m != nil {
			lines[i] = "<b>" + m[1] + "</b>"
		} else if m := reBullet.FindStringSubmatch(s); m != nil {
			lines[i] = "• " + m[1]
		}
	}
	text = strings.Join(lines, "\n")

	// 4. Inline code stashed too, so bold/strike can't rewrite its content.
	text = reInline.ReplaceAllStringFunc(text, func(m string) string {
		return put("<code>" + reInline.FindStringSubmatch(m)[1] + "</code>")
	})
	text = reLink.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = reBold.ReplaceAllString(text, "<b>$1</b>")
	text = reStrike.ReplaceAllString(text, "<s>$1</s>")

	// 5. Restore stashed fences and inline code.
	for i, s := range stash {
		text = strings.Replace(text, fmt.Sprintf(stashMark, i), s, 1)
	}

	// 6. Pipe tables → aligned <pre> (runs last: cells already inline-formatted,
	// renderPipeTable strips tags since Telegram doesn't render them in <pre>).
	text = formatPipeTables(text)

	if len(tools) > 0 {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		text += "\n\n<code>" + strings.Join(names, " → ") + "</code>"
	}
	return text
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// formatPipeTables converts runs of |…| lines into aligned <pre> blocks.
func formatPipeTables(text string) string {
	var out, table []string
	flush := func() {
		if len(table) > 0 {
			out = append(out, renderPipeTable(table))
			table = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "|") && strings.HasSuffix(s, "|") && len(s) > 1 {
			if !reDivider.MatchString(s) { // drop |---|---| rows
				table = append(table, s)
			}
			continue
		}
		flush()
		out = append(out, line)
	}
	flush()
	return strings.Join(out, "\n")
}

// renderPipeTable pads cells to column width, ─-rule under the header.
func renderPipeTable(rows []string) string {
	var cells [][]string
	var widths []int
	for _, row := range rows {
		parts := strings.Split(row, "|")
		parts = parts[1 : len(parts)-1] // drop empty edges outside the pipes
		r := make([]string, len(parts))
		for i, c := range parts {
			r[i] = strings.TrimSpace(reTag.ReplaceAllString(c, ""))
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := len([]rune(r[i])); n > widths[i] {
				widths[i] = n
			}
		}
		cells = append(cells, r)
	}
	var b strings.Builder
	b.WriteString("<pre>")
	for ri, row := range cells {
		if ri > 0 {
			b.WriteString("\n")
		}
		for i, c := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c + strings.Repeat(" ", widths[i]-len([]rune(c))))
		}
		if ri == 0 && len(cells) > 1 {
			b.WriteString("\n")
			for i, w := range widths {
				if i > 0 {
					b.WriteString("  ")
				}
				b.WriteString(strings.Repeat("─", w))
			}
		}
	}
	b.WriteString("</pre>")
	return b.String()
}

// chunkHTML splits html into ≤max-byte chunks at the last close-tag
// boundary, else last newline, else a hard cut. Port of Crow's
// _safe_html_chunks. max is bytes: ≥ char count in UTF-8, so a 4000-byte
// cap always satisfies Telegram's 4096-char limit.
func chunkHTML(html string, max int) []string {
	if len(html) <= max {
		return []string{html}
	}
	var chunks []string
	for pos := 0; pos < len(html); {
		end := pos + max
		if end >= len(html) {
			chunks = append(chunks, html[pos:])
			break
		}
		safe := end
		if i := strings.LastIndex(html[pos:end], "</"); i > 0 {
			if c := strings.Index(html[pos+i:], ">"); c != -1 && pos+i+c < end {
				safe = pos + i + c + 1
			}
		} else if i := strings.LastIndex(html[pos:end], "\n"); i > 0 {
			safe = pos + i + 1
		}
		chunks = append(chunks, html[pos:safe])
		pos = safe
	}
	return chunks
}
