# OpenAssist setup

v1 setup guide: server (Dokploy), Telegram channel, Google connector, Mac app,
backups. Design rationale and phase records live in the design records under
[docs/design/](design/).

## 1. Requirements

- **Server:** a Linux server with Dokploy installed (single instance — `replicas: 1`
  is required; the routine scheduler cannot be replicated).
- **Engine identity (server):** the container has no keychain, so the identity is
  provided via env — **one** of the two below. The Claude Code CLI is baked into the
  image at a pinned version; no separate install is needed:
  - `CLAUDE_CODE_OAUTH_TOKEN` — to run on a subscription. Run
    `claude setup-token` on your own Mac and enter the long-lived token it prints.
  - `CLAUDE_API_KEY` — a pay-as-you-go Anthropic API key.

  > **None of this is needed on a local Mac:** if you have run `claude login`, the
  > identity is read from the keychain. (The entry is stored under the username, so
  > `USER` is passed through to the engine environment — see `docs/spike-notlari.md`.)
- **Optional:** a Telegram bot token (BotFather), a Google Cloud OAuth client, a Mac
  running macOS 14+ (for the local limb).

## 2. Server (Dokploy)

1. In Dokploy, create a new **Compose** service; point the repository at
   `github.com/esmworks/OpenAssist` and the compose file at
   `deploy/compose.yml`.
2. Enter the environment variables:

   | Variable | Required | Description |
   |---|---|---|
   | `POSTGRES_PASSWORD` | yes | DB password (internal to compose) |
   | `API_TOKEN` | yes | the lock on the private API — `openssl rand -hex 32`. The same value is entered in the Mac app. At least 32 characters; otherwise core will not start |
   | `PI_PROVIDER_ID` / `PI_PROVIDER_BASE_URL` / `PI_PROVIDER_KEY` / `PI_PROVIDER_API` / `PI_MODELS` | no | **first-start seed only.** Providers live in the database and are managed on the Mac under Settings › **Models** (Ayarlar › Modeller); these variables are imported once while the provider table is completely empty, after which the record on screen is authoritative |
   | `PI_DEFAULT_MODEL` | no | model used when a bot does not pick one (`sağlayıcı/model`) |
   | `PI_THINKING` | no | `off` · `low` · `medium` · `high` |
   | `PI_ENV` | no | pi's built-in provider keys: comma-separated `ad=değer` (e.g. `ANTHROPIC_API_KEY=sk-…,OPENAI_API_KEY=sk-…`) |
   | `MASTER_KEY` | if a provider key or Google is used | `openssl rand -hex 32` — sealing. If unset, API keys cannot be saved from the Models screen (they are never stored in plain text) |
   | `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | no | Google is disabled if empty |
   | `PUBLIC_BASE_URL` | if Google is used | the domain routed to :8081 (redirect root) |
   | `TELEGRAM_BOT_TOKEN` | no | Telegram is disabled if empty |
   | `TELEGRAM_ALLOWED_CHAT_IDS` | if Telegram is used | comma-separated chat_id — fail-closed |

3. Deploy. Migrations run automatically on start (`MIGRATE_ON_START`); on first
   start the seed bots are created (**Kişisel Asistan** — Personal Assistant, and
   **Gelen Kutusu** — Inbox).
4. **Domain + TLS (Dokploy UI › Domains).** Do not write Traefik labels by hand.
   Create **two entries** for the same domain — there are two HTTP surfaces and both
   are required:

   | Path | Port | What it serves |
   |---|---|---|
   | `/` | **8080** | private API — the Mac app connects here (protected by `API_TOKEN`) |
   | `/oauth` | **8081** | Google OAuth endpoints only (Google redirects here) |

   Traefik prioritizes the longer (more specific) rule, so `/oauth` requests go to
   8081 and everything else goes to 8080. **Path strip must be OFF** — the
   publichttp endpoint expects the full path (`/oauth/google/callback`). Keep HTTPS +
   Let's Encrypt enabled: the token travels in the `Authorization` header and could be
   sniffed over plain HTTP.

   > If you don't use Google, don't create the second entry at all; a single `/` → 8080
   > is enough. If path-based routing causes trouble, the alternative is a separate
   > subdomain for OAuth (`oauth.example.com` → 8081) with `PUBLIC_BASE_URL`
   > pointing to it.
5. Health: the container is probed with its own `core healthcheck` command; logs are
   in JSON format (`LOG_FORMAT=json`).

### Surfaces and access

| Port | Surface | Exposed? | Auth |
|---|---|---|---|
| 8080 | private API: bot/run/approval/routine/memory, SSE, runner WS | yes (domain) | `Authorization: Bearer $API_TOKEN` |
| 8081 | public: Google OAuth start/callback | yes (domain `/oauth`) | the OAuth flow itself |
| 8082 | MCP: `/mcp`, `/mcp/mac` — the engine's tool surface | **no** (inside the container, 127.0.0.1) | none, not needed |

`/healthz` requires no token: the container's health probe goes through it. The only
things it leaks are the version string and the DB status.

The MCP surface is deliberately on a separate listener. If it shared a listener with
the private API, either the bot management tools would be exposed to the internet as
soon as it was bound to the domain, or the token would have to be written into the MCP
configuration of all four engines — spreading the token needlessly.

## 3. Telegram channel (optional, K16)

1. Create a bot with BotFather and put the token in `TELEGRAM_BOT_TOKEN`.
2. Find your own chat_id (e.g. via @userinfobot) and put it in
   `TELEGRAM_ALLOWED_CHAT_IDS`. Telegram does NOT start without an allowlist; anyone
   not on the list is silently rejected.
3. Message the bot — it becomes a run on the active bot; approval cards are resolved
   with a single tap ("Always allow" writes a rule; K17 vetoes still apply).

> Privacy cost (deliberate): the Bot API is not end-to-end encrypted; content sent to
> Telegram passes through Telegram's servers.

## 4. Google connector (optional)

1. Google Cloud Console → create an OAuth client (Web); Authorized redirect
   URI: `<PUBLIC_BASE_URL>/oauth/google/callback` — e.g.
   `https://asistan.example.com/oauth/google/callback`. `PUBLIC_BASE_URL`
   must be a full URL including the scheme (`https://…`); a value without a scheme is
   rejected at startup, because Google matches redirect_uri literally.
2. Set the `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `MASTER_KEY` and
   `PUBLIC_BASE_URL` env vars and redeploy.
3. Connecting is done from chat (K13 — setup is a conversation): tell the bot
   "connect Gmail", open the returned link in a browser and grant access. The refresh
   token is stored in the DB sealed with AES-256-GCM.
4. Losing `MASTER_KEY` does not lose data: connectors are reconnected and provider
   keys are re-entered from the Models screen.

## 5. Mac app (local limb, K2)

```bash
cd mac && swift build -c release
.build/release/OpenAssistMac
```

- **Connecting to the server.** Settings › Connections (Ayarlar › Bağlantılar):
  - **Address:** the domain root — e.g. `https://asistan.example.com` (the
    `/` → 8080 entry from §2.4). Locally, `http://127.0.0.1:8080`.
  - **API token:** exactly the same value as `API_TOKEN` on the server.
    It is stored in the Keychain, not in UserDefaults. Every request and the runner WS
    carry it in the `Authorization: Bearer …` header.

  The status line distinguishes three states: **Connected**, **Invalid token** (address
  correct, token wrong/missing) and **Cannot connect** (server unreachable).

  > The domain is the setup path; compose does NOT publish ports to the host. A host
  > port is a global resource — if it were published, a second install on the same
  > machine could not be deployed ("port is already allocated"). If you need a
  > temporary tunnel for diagnostics, connect to the container's network address:
  >
  > ```bash
  > docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' <core-container>
  > ```

- **If you lose the token**, generate a new one (`openssl rand -hex 32`), change
  `API_TOKEN` in the Dokploy env, redeploy and enter the new value in the app. No data
  is lost.
- While the app is closed, the server bots keep running; the Mac tools
  (`read_file`, `write_file`, `list_dir`, `trash_item`) are visible only while
  connected and disappear on disconnect (K2 — dynamic MCP surface).
- Security: nothing can escape the allowed root directories (symlink escapes
  included); there is no permanent delete — only moving to the Trash.
- Signing/notarization: requires an Apple Developer account (a user step; a local
  build is enough for now).

## 6. Backups

In the host cron (on the Dokploy server):

```bash
15 3 * * * cd /path/to/openassist/deploy && ./backup.sh >> ../files/backups/backup.log 2>&1
```

`backup.sh`: `pg_dump | gzip`, 14-day retention. The restore command is in the comment
at the top of the script. The `../files/` root (pgdata + data + backups) should be
included in the server backup.

## 7. Operations

- **Activity:** `GET /api/activity` — runs + Mac operations in a single stream (K19).
- **Providers:** `GET/POST /api/providers`, `PUT/DELETE
  /api/providers/{id}` — provider definitions and model lists. On the Mac: Settings ›
  **Models**. The API key is **write-only**: it is stored sealed and never returned in
  any response; the client sees only `has_key`. If `api_key` is omitted from the body,
  the stored key is left untouched; if an empty string is sent, it is deleted.
  Model suggestions come from `GET /api/models/catalog` (models.dev, cached on the
  server for 24 hours, `?refresh=1` forces a refresh) — the catalog is a convenience,
  not an authority: a model name can always be typed by hand.
- **Engine:** `GET /api/engines` returns the engine **with its status** — whether pi is
  installed, its version and the selectable model list measured from
  `pi --list-models`. The probe is cached for 5 min, `?refresh=1` forces a refresh; the
  cache is dropped automatically when a provider changes. Per-bot selection is
  `PATCH /api/bots/{id}` (`engine` + `model`) — Settings › Bots (Ayarlar › Botlar). An
  unrecognized engine returns 400.
- **Task board:** `GET /api/tasks`; **bulk approval:** `POST /api/approvals/bulk`
  (execute/tainted/questions are never bulk-approved).
- **Counters:** `GET /api/stats?days=7` — runs/cost/tokens, broken down by bot.
- **Drain:** on deploy, SIGTERM → active runs finish within 60 s, or those waiting for
  approval are parked; no half-finished work is lost.

## 8. Local development

```bash
cp .env.example .env
make build && make test          # unit tests
make lint                        # golangci-lint
docker run -d --name oa-test-pg -e POSTGRES_PASSWORD=test \
  -e POSTGRES_DB=openassist -p 55432:5432 postgres:18-alpine
DATABASE_URL='postgres://postgres:test@localhost:55432/openassist?sslmode=disable' \
  make test-integration          # -p 1: tests use the DB sequentially
cd mac && swift test             # Swift runner tests (local, not in CI)
```
