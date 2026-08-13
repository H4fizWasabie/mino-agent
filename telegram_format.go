package main

// Markdown → Telegram HTML formatting.
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
	reFence     = regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
	reHeading   = regexp.MustCompile(`^#{1,3}\s+(.+)$`)
	reBullet    = regexp.MustCompile(`^[-*]\s+(.+)$`)
	reInline    = regexp.MustCompile("`([^`\n]+)`")
	reLink      = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBold      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic    = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`) // single * pair, bold already consumed
	reUnderline = regexp.MustCompile(`__([^_\n]+)__`)         // double underscore
	reSpoiler   = regexp.MustCompile(`\|\|(.+?)\|\|`)         // Telegram spoiler
	reStrike    = regexp.MustCompile(`~~(.+?)~~`)
	reQuote     = regexp.MustCompile(`^>\s?(.*)$`)
	reQuoteExp  = regexp.MustCompile(`^>!\s?(.*)$`)
	reDivider   = regexp.MustCompile(`^\|[\s\-:|]+\|$`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
)

const stashMark = "\x00STASH%d\x00"

// sendTelegramReply is the single exit point for outbound Telegram text:
// section-split on --- lines → format → chunk → send. Each section threads
// to the previous one (the caller's message for the first) so multi-part
// replies read as one chain. A chunk that Telegram rejects (malformed HTML →
// 400) is resent as plain text — stray tags beat a lost message.
func sendTelegramReply(bot *tgbotapi.BotAPI, chatID int64, reply string, tools []ToolCall, replyTo int) {
	sections := splitSections(reply)
	lastID := replyTo
	for i, section := range sections {
		var t []ToolCall
		if i == len(sections)-1 {
			t = tools // the tool trail belongs on the final section only
		}
		html := formatTelegramHTML(section, t)
		for _, chunk := range chunkHTML(html, 4000) {
			msg := tgbotapi.NewMessage(chatID, chunk)
			msg.ParseMode = tgbotapi.ModeHTML
			if lastID != 0 {
				msg.ReplyToMessageID = lastID
			}
			sent, err := bot.Send(msg)
			if err != nil {
				slog.Warn("telegram html send failed, retrying plain", "error", err)
				plain := tgbotapi.NewMessage(chatID, chunk)
				if lastID != 0 {
					plain.ReplyToMessageID = lastID
				}
				if sent2, err2 := bot.Send(plain); err2 == nil {
					sent = sent2
				} else {
					slog.Warn("telegram plain send failed", "error", err2)
				}
			}
			if sent.MessageID != 0 {
				lastID = sent.MessageID
			}
		}
	}
}

// splitSections splits a reply on lines that are exactly "---". Telegram has
// no horizontal-rule tag, so the divider becomes a section break and each
// section ships as its own message (issue #181).
func splitSections(reply string) []string {
	var sections []string
	var cur []string
	flush := func() {
		s := strings.TrimSpace(strings.Join(cur, "\n"))
		if s != "" {
			sections = append(sections, s)
		}
		cur = nil
	}
	for _, line := range strings.Split(reply, "\n") {
		if strings.TrimSpace(line) == "---" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	if len(sections) == 0 {
		return []string{""}
	}
	return sections
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

	// 2. Blockquotes: consecutive >-lines group into one block; >! is the
	// expandable variant. Inner content is escaped now (inline formatting
	// inside quotes stays literal — ponytail: quotes are plain prose).
	text = formatBlockquotes(text, put)

	// 3. Escape everything else.
	text = escapeHTML(text)

	// 4. Line pass: headings → <b>, list items → •.
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

	// 5. Inline code stashed too, so bold/strike can't rewrite its content.
	text = reInline.ReplaceAllStringFunc(text, func(m string) string {
		return put("<code>" + reInline.FindStringSubmatch(m)[1] + "</code>")
	})
	text = reLink.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = reBold.ReplaceAllString(text, "<b>$1</b>")
	text = reItalic.ReplaceAllString(text, "$1<i>$2</i>") // preserve the leading non-* char
	text = reUnderline.ReplaceAllString(text, "<u>$1</u>")
	text = reSpoiler.ReplaceAllString(text, `<tg-spoiler>$1</tg-spoiler>`)
	text = reStrike.ReplaceAllString(text, "<s>$1</s>")

	// 6. Restore stashed fences, blockquotes, and inline code.
	for i, s := range stash {
		text = strings.Replace(text, fmt.Sprintf(stashMark, i), s, 1)
	}

	// 7. Pipe tables → aligned <pre> (runs last: cells already inline-formatted,
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

// formatBlockquotes groups consecutive >-lines into <blockquote> blocks
// (">!" starts the expandable variant) and stashes them so the escape pass
// can't touch the tags (issue #181). Inner content is escaped at build time;
// inline formatting inside quotes stays literal (ponytail: quotes are prose).
func formatBlockquotes(text string, put func(string) string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var block []string
	expandable := false
	flush := func() {
		if len(block) == 0 {
			return
		}
		tag := "<blockquote>"
		if expandable {
			tag = "<blockquote expandable>"
		}
		out = append(out, put(tag+escapeHTML(strings.Join(block, "\n"))+"</blockquote>"))
		block = nil
		expandable = false
	}
	for _, line := range lines {
		if m := reQuoteExp.FindStringSubmatch(line); m != nil {
			expandable = true
			block = append(block, m[1])
			continue
		}
		if m := reQuote.FindStringSubmatch(line); m != nil {
			block = append(block, m[1])
			continue
		}
		flush()
		out = append(out, line)
	}
	flush()
	return strings.Join(out, "\n")
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
// boundary, else last newline, else a hard cut.
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
