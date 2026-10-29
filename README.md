# OpenAssist

A personal bot/agent system: bots that live on a server, keep working while your computer is off,
and use your Mac as a first-class limb. Engine: **pi** — the provider and model are your choice (any
OpenAI/Anthropic-compatible endpoint) and are managed from the app's **Models** screen: provider
records live in the database, the model list is picked from the models.dev catalog, and keys are
stored sealed. The permission gate and the OpenAssist tools come from the `oa-gateway` extension
(PRD decision K18).

- **Setup:** [docs/kurulum.md](docs/kurulum.md)
- **Design records:** [docs/design/](docs/design/)

Decision codes such as K17 refer to the decisions of the original product requirements document,
which is not published; the design records summarize the ones that matter for each area.

## What it does (v1)

- **Bots + queue:** each bot runs on the server with its own identity, system prompt, tool set and
  persistent memory; runs are queued and drain safely.
- **Approval gate (K17):** every tool with side effects asks a human first; "always allow / always
  deny" rules, a daily action quota, quiet hours, taint (automatic approval is void in a run that
  has read external content), and never a rule for the execute class.
- **Park/resume (K15):** if the approval window expires the run parks; once the approval arrives it
  resumes where it left off — an answer that comes hours later is fine.
- **Telegram (K16):** chat with an allowlist; approval cards in a single tap, the streaming message
  updates live. Optional — the privacy cost is documented.
- **Connectors (K13):** Gmail/Calendar through the MCP gateway; setup is a conversation ("connect
  Gmail" → OAuth link). Tokens are sealed with AES-256-GCM.
- **Mac limb (K2):** the SwiftUI app opens an outbound WS to the server; Mac tools are visible only
  while it is connected. Path confinement and no permanent deletion.
- **Routines (K14):** cron + time zone in the DB; a single-instance scheduler, a missed-routine
  policy (`fail` | `warn_stale`), the five-section K12 report.
- **Surfaces (K19):** activity feed (runs + Mac operations), task board, safe bulk approval,
  `/api/stats`.

## Layout

```
cmd/core/            Go entry point: serve | migrate | healthcheck | version
internal/config/     typed env loading + validation
internal/orchestrator/  queue, run state machine, approval gate
internal/policy/     pure decision core (K17 veto order) — table-tested
internal/engine/     engine contract + pi adapter (RPC + oa-gateway extension) + enginetest
internal/gateway/    MCP gateway: connectors + management + Mac tools
internal/coretools/  K13 management tools (bot/routine/memory/skill)
internal/routines/   routine scheduler (cron, FOR UPDATE SKIP LOCKED)
internal/connectors/ Google OAuth + Gmail/Calendar
internal/telegram/   Telegram gateway (channel, approval cards)
internal/runner/     Mac runner hub (WS protocol + local_audit)
internal/httpapi/    private API (:8080) — SSE, surfaces, runner WS endpoint
internal/publichttp/ public surface (:8081) — webhook/OAuth only
internal/crypto/     AES-256-GCM sealing (MASTER_KEY)
internal/store/      pgx + sqlc + goose (internal/store/db is generated, do not edit)
db/                  migrations (goose) + queries (sqlc source)
deploy/              Dockerfile + Dokploy compose + backup.sh
mac/                 SwiftUI client + runner (SPM, zero dependencies)
```

## Development

```bash
cp .env.example .env        # fill in your local Postgres settings
make build                  # bin/core
make test                   # unit tests
make test-integration       # needs DATABASE_URL (-p 1, sequential DB)
make lint                   # golangci-lint
make generate               # sqlc — after changing db/queries
```

Adding a query: write a goose migration in `db/migrations/` + the SQL in `db/queries/` →
`make generate` → the typed Go function is ready in `internal/store/db`.

End-to-end scenario (Telegram → run → approval → Mac → audit → activity): `internal/e2e/` — runs
with a fake engine and a fake Bot API, against real Postgres + orchestrator + gateway + WS runner.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

OpenAssist is licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for
third-party components and trademark terms.
