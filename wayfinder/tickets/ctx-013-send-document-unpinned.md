# Context Truth — send_document unreachable under the schema cap

Status: **OPEN** (GitHub issue #155)

## Question

Why did the model use the insecure bash+curl workaround again instead of the native send_document tool?

## Evidence (2026-08-11 full-suite live test, phases 7-8)

- tool_calls row: bash args contain the bot token (`BOT_TOKEN="8905..."`) — the curl sendDocument flow from the memory note `telegram_send_document_curl_workaround`
- `send_document` is registered but NOT in `essentialToolNames` — the per-session 20-tool schema cap can evict it, and keyword selection did not surface it for "send it to my telegram"
- The memory note actively re-teaches the insecure path, so the model follows it

## Design sketch

- Add `send_document` to `essentialToolNames` (pinned like `send_message`)
- Update or annotate the `telegram_send_document_curl_workaround` memory note to prefer the native tool
- Acceptance: a "send file to telegram" turn uses send_document (visible in tool_calls); no bot token in any bash args; the file still arrives
