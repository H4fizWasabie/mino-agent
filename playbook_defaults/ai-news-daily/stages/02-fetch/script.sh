#!/bin/bash
# 02-fetch script stage (PA-005 pilot, issue #306) — fetch the stories
# 01-judgment selected and extract facts. ZERO inference: the harness
# executes this script directly; there is no model call, no `mino exec`,
# no notification of any kind.
#
# Contract:
#   input  : ../01-judgment/output/topics.md   (## Title / Source: URL / Key claim)
#   output : output/facts.md                   (## Title / Source: / Status: / Facts:)
#   fail-fast: if ALL stories fail to fetch, exit 1 and the run fails loudly.
#   A single dead story is recorded as "Status: fetch failed" with the
#   reason — never silently dropped (the synthesis stage reports it as
#   unverified). Never silent either way.
#
# Extraction is deliberately heuristic (title + lead paragraphs, boilerplate
# lines skipped): a committed, reviewed artifact — the pilot measures it and
# the data decides any refinement.
#
# Environment (harness): PATH, HOME, TZ, LANG only. No secrets.
set -uo pipefail

# The harness runs this script with cwd = the RUN-scoped stage dir
# (runs/<id>/stages/02-fetch) — $0 is the definition path, never use it
# for data paths.
STAGE_DIR="$(pwd)"                              # runs/<id>/stages/02-fetch (harness cwd)
TOPICS="$STAGE_DIR/../01-judgment/output/topics.md"
OUT_DIR="$STAGE_DIR/output"
OUT="$OUT_DIR/facts.md"
UA="Mozilla/5.0 (compatible; mino-ai-news-pilot/1.0)"
mkdir -p "$OUT_DIR"
: > "$OUT"

# parse_topics: emit "<title>TAB<url>" per story block ("" url = block missing Source).
parse_topics() {
  awk '
    /^## / {
      if (title != "") { print title "\t" url }
      title = substr($0, 4)
      url = ""
      next
    }
    /^Source: / && title != "" { url = substr($0, 9); sub(/[ \t\r]+$/, "", url) }
    END { if (title != "") print title "\t" url }
  ' "$TOPICS"
}

# extract_page: html file -> title line, then lead text lines (stdlib python3).
extract_page() {
  python3 - "$1" <<'PY'
import html.parser, re, sys
class P(html.parser.HTMLParser):
    def __init__(self):
        super().__init__()
        self.skip = 0
        self.in_title = False
        self.title = None
        self.text = []
    def handle_starttag(self, tag, attrs):
        if tag == "title":
            self.in_title = True
        if tag in ("script", "style", "noscript", "svg"):
            self.skip += 1
    def handle_endtag(self, tag):
        if tag == "title":
            self.in_title = False
        if tag in ("script", "style", "noscript", "svg") and self.skip:
            self.skip -= 1
    def handle_data(self, d):
        t = " ".join(d.split())
        if not t:
            return
        if self.in_title:
            if self.title is None:
                self.title = t[:200]
        elif not self.skip:
            self.text.append(t)
p = P()
p.feed(open(sys.argv[1], encoding="utf-8", errors="replace").read())
if p.title:
    print(p.title)
nav = re.compile(r"^(menu|sign in|subscribe|cookie|newsletter|advertise|search|home|about|contact)$", re.I)
for line in p.text:
    if len(line) < 20:
        continue
    if nav.match(line):
        continue
    print(line[:400])
PY
}

FETCHED=0
while IFS=$'\t' read -r title url; do
  [ -n "$title" ] || continue
  if [ -z "$url" ]; then
    printf '## %s\nSource: (missing)\nStatus: fetch failed (no URL in topics.md)\n\n' "$title" >> "$OUT"
    continue
  fi
  case "$url" in
    http://*|https://*) ;;
    *)
      printf '## %s\nSource: %s\nStatus: fetch failed (invalid URL)\n\n' "$title" "$url" >> "$OUT"
      continue ;;
  esac

  html="$STAGE_DIR/.fetch.tmp"
  ok=0
  for attempt in 1 2; do
    if curl -sS -L --compressed --max-time 20 -A "$UA" "$url" -o "$html" 2>/dev/null && [ -s "$html" ]; then
      ok=1
      break
    fi
    sleep 3
  done
  if [ "$ok" != 1 ]; then
    printf '## %s\nSource: %s\nStatus: fetch failed (HTTP/network error after 2 attempts)\n\n' "$title" "$url" >> "$OUT"
    continue
  fi
  text="$(extract_page "$html")"
  rm -f "$html"
  if [ -z "$text" ]; then
    printf '## %s\nSource: %s\nStatus: fetch failed (empty page)\n\n' "$title" "$url" >> "$OUT"
    continue
  fi
  # Line 1 = page title; lines 2-11 = lead paragraphs -> facts.
  facts="$(printf '%s\n' "$text" | sed -n '2,11p')"
  if [ -z "$facts" ]; then
    printf '## %s\nSource: %s\nStatus: fetch failed (no extractable text)\n\n' "$title" "$url" >> "$OUT"
    continue
  fi
  {
    printf '## %s\nSource: %s\nStatus: fetched\nFacts:\n' "$title" "$url"
    printf '%s\n' "$facts" | sed 's/^/- /'
    printf '\n'
  } >> "$OUT"
  FETCHED=$((FETCHED + 1))
done < <(parse_topics)

if [ ! -s "$OUT" ]; then
  echo "ERROR: no stories processed (missing or empty topics.md)" >&2
  exit 1
fi
if [ "$FETCHED" -lt 1 ]; then
  echo "ERROR: all stories failed to fetch — nothing to synthesize" >&2
  exit 1
fi
echo "fetched=$FETCHED stories=$(grep -c '^## ' "$OUT")"
exit 0
