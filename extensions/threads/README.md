# threads-extension — Mino ↔ Threads

Posts to Threads via the official API. Implements Mino extension protocol.

## Setup (once, ~10 min)

### 1. Create a Meta app

- Go to https://developers.facebook.com/
- Create app → "Consumer" or "Business"
- Add "Threads API" product
- Configure Threads → set redirect URI to `http://localhost:9200/callback`

### 2. Set env vars

```bash
export THREADS_APP_ID="your-meta-app-id"
export THREADS_APP_SECRET="your-meta-app-secret"
# optional:
export THREADS_PORT="9200"
export THREADS_REDIRECT_URI="http://localhost:9200/callback"
```

### 3. Run it

```bash
cd extensions/threads
go build -o threads-extension .
./threads-extension &
```

### 4. Authorize

Open `http://localhost:9200/auth` in a browser.
Log into Threads, approve permissions.
Token saved to `~/.mino/threads_token.json` (60-day, auto-refreshed).

### 5. Register with Mino

Add to `~/.mino/extensions.json`:
```json
[{"name": "threads", "url": "http://localhost:9200"}]
```

Restart Mino or use `reload_plugins` tool.

## Tools

| Tool | Description |
|------|------------|
| `threads_post` | Publish text + optional image. Can reply to existing thread. |
| `threads_get_replies` | Get replies to your thread. |

## Token lifecycle

- Initial token: 60 days (long-lived exchange)
- Auto-refresh on expiry (refresh endpoint)
- Falls back to /auth if refresh fails
