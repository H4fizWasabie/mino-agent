package main

import (
	"strings"
	"testing"
)

func TestFormatTelegramHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello", "hello"},
		{"bold", "**hi**", "<b>hi</b>"},
		{"strike", "~~gone~~", "<s>gone</s>"},
		{"inline code", "run `ls -la` now", "run <code>ls -la</code> now"},
		{"link", "[docs](https://x.dev)", `<a href="https://x.dev">docs</a>`},
		{"heading", "## Plan", "<b>Plan</b>"},
		{"bullets", "- one\n* two", "• one\n• two"},
		{"escape", "<script>alert(1)</script> & co", "&lt;script&gt;alert(1)&lt;/script&gt; &amp; co"},
		{"code protects inline", "`**x**`", "<code>**x**</code>"},
		{"escaped char in code", "use `a < b` here", "use <code>a &lt; b</code> here"},
		{"fence with lang", "```go\na < b\n```", "<pre><code class=\"language-go\">a &lt; b\n</code></pre>"},
		{"fence no lang", "```\nx\n```", "<pre><code>x\n</code></pre>"},
		{"fence protects markdown", "```\n**x** <b>\n```", "<pre><code>**x** &lt;b&gt;\n</code></pre>"},
		{"bold inside bullet", "- **hot**", "• <b>hot</b>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatTelegramHTML(c.in, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestFormatTelegramHTMLToolTrail(t *testing.T) {
	got := formatTelegramHTML("hi", []ToolCall{{Name: "read"}, {Name: "write"}})
	want := "hi\n\n<code>read → write</code>"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestFormatPipeTables(t *testing.T) {
	in := "| Item | Qty |\n|------|-----|\n| Cement | 10 |"
	want := "<pre>Item    Qty\n──────  ───\nCement  10 </pre>"
	if got := formatTelegramHTML(in, nil); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestPipeTableEdges(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"text around table", "before\n| A | B |\n| 1 | 2 |\nafter",
			"before\n<pre>A  B\n─  ─\n1  2</pre>\nafter"},
		{"tags stripped in cells", "| **A** | B |\n| 1 | 2 |",
			"<pre>A  B\n─  ─\n1  2</pre>"},
		{"single row no rule", "| A | B |",
			"<pre>A  B</pre>"},
		{"not a table", "1 | 2", "1 | 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatTelegramHTML(c.in, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestChunkHTML(t *testing.T) {
	if got := chunkHTML("short", 4000); len(got) != 1 || got[0] != "short" {
		t.Fatalf("under-limit input must be single chunk, got %v", got)
	}

	html := strings.Repeat("<b>xxxxxxxx</b>\n", 100) // 1600 bytes
	chunks := chunkHTML(html, 500)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c) > 500 {
			t.Errorf("chunk %d exceeds max: %d bytes", i, len(c))
		}
		if strings.Count(c, "<b>") != strings.Count(c, "</b>") {
			t.Errorf("chunk %d splits a tag pair: %q", i, c)
		}
	}
	if strings.Join(chunks, "") != html {
		t.Error("chunks must reassemble to original")
	}
}

func TestFormatTelegramHTMLGaps(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"italic", "a *word* here", "a <i>word</i> here"},
		{"underline", "double __emphasis__", "double <u>emphasis</u>"},
		{"spoiler", "secret ||hidden|| text", "secret <tg-spoiler>hidden</tg-spoiler> text"},
		{"blockquote", "> quoted line", "<blockquote>quoted line</blockquote>"},
		{"blockquote multi", "> one\n> two", "<blockquote>one\ntwo</blockquote>"},
		{"blockquote expandable", ">! reveal on tap", "<blockquote expandable>reveal on tap</blockquote>"},
		{"quote escapes inner", "> a < b", "<blockquote>a &lt; b</blockquote>"},
		{"quote ends at plain line", "> one\nplain", "<blockquote>one</blockquote>\nplain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatTelegramHTML(c.in, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSplitSections(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "just text", []string{"just text"}},
		{"two sections", "part one\n---\npart two", []string{"part one", "part two"}},
		{"trimmed", "part one\n\n---\n\npart two", []string{"part one", "part two"}},
		{"keeps inner blanks", "a\n\nb", []string{"a\n\nb"}},
		{"trailing divider", "text\n---", []string{"text"}},
		{"divider only", "---", []string{""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitSections(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("splitSections(%q) = %q, want %q", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("splitSections(%q) = %q, want %q", c.in, got, c.want)
				}
			}
		})
	}
}
