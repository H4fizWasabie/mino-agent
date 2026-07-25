# Onboarding flow

Status: resolved
Type: grilling
Blocked by: —

## Question

Replace the blank config form with a guided onboarding: provider logo buttons (ChatGPT, Claude, "I have a key"), OAuth popup or paste-key, auto-open browser, optional Telegram QR code, and a "Hi Mino!" first prompt.

## Answer

1. **Provider buttons:** Dashboard frontend shows a button grid on first run (when `needs_onboarding` is true). Buttons: "ChatGPT" (→ existing `/api/oauth/device/` Codex device flow), "Claude" (→ existing `/api/oauth/login/` PKCE), "I have my own key" (→ existing manual API key form). Manual form becomes the "advanced" toggle — hidden behind a link, not the default.

2. **Auto-open browser:** After `RunDashboard` starts in `main.go`, sleep 500ms, detect OS (`runtime.GOOS`), run `xdg-open http://localhost:7779` (Linux) or `open http://localhost:7779` (macOS). Already have OS detection pattern in `update.go`.

3. **Telegram setup:** Post-onboarding card in dashboard: "Set up Telegram?" with instructions (create bot via @BotFather, paste token). No QR code — avoids a JS library dependency for a one-time step. Paste field saves to `TELEGRAM_BOT_TOKEN` in `mino.env`.

4. **First prompt:** After setup completes, the chat input gets placeholder text "Hi Mino! What can you do?" and is auto-focused. Not auto-sent — user clicks send.

5. **State tracking:** `needsOnboarding()` already works (checks for providers.json, env API key, stored auth). No new sentinel needed.

File list: `main.go` (auto-open browser), `dashboard.go` (onboarding UI state to frontend via existing `/api/data`), dashboard HTML/JS (button grid, Telegram card, first-prompt affordance).
