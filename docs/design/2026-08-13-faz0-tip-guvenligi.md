# Phase 0 design record — Skeleton and the type-safety chain

Date: 2026-08-13 · Status: accepted · Related: PRD K6, K15, K18, Appendix B

## Question

The user's question: "A Prisma/Zod equivalent for Postgres — let's not skip type safety at any
layer." Which tools give us, on the Go side, the guarantees we have in the TypeScript world?

## Requirements

- **Compile time:** no SQL queries as hand-written strings + `interface{}` scanning; a query that
  breaks when the schema changes must be caught **at compile time** (the guarantee Prisma gives).
- **Runtime boundaries:** all data coming in from outside (env, HTTP body, engine stream-json
  output, Telegram update) enters a typed structure **only after validation** (the guarantee Zod
  gives).
- **Single binary:** migrations are embedded in the binary — the Dokploy container needs no
  external tool.
- **Small dependency surface:** a one-person project; we don't want ORM runtime magic.

## Decisions and equivalence map

| TS world | Chosen in Go | Rejected alternatives and why |
|---|---|---|
| Prisma (typed queries) | **sqlc** — you write the SQL, it generates the Go types; a query/schema mismatch blows up **at generation time** | GORM/ent: runtime reflection + their own DSL, control over SQL is lost. sqlx: typed but manual scanning, the compiler doesn't catch schema mismatches |
| Prisma Migrate | **goose** (as a library, embedded via `embed.FS`) — `core migrate` subcommand + `MIGRATE_ON_START` | golang-migrate: equivalent; goose's single-file Up/Down format and library API are simpler. Atlas: declarative schema — overkill for a small project |
| DB driver | **pgx/v5** (pool: pgxpool) — Postgres-native, faster than `database/sql` and typed | lib/pq: unmaintained |
| Zod (env) | **caarlos0/env** + `Validate()` — typed struct, missing/malformed env is an error at startup | Viper: config file layers are unnecessary; 12-factor env is enough (Dokploy) |
| Zod (enum) | Typed string constants + `Valid()`/converter function — **an unknown value is an error, no silent default** | iota enum: unreadable in DB/JSON |
| Zod (API DTO) | `json.Decoder` + `DisallowUnknownFields` + per-field `Validate()` (arrives with the API in Phase 2) | go-playground/validator: tag magic; added if the need arises |
| Zod (engine output) | **Tolerant** parse + `raw` passthrough (Appendix B.2-1): known fields are typed, an unknown event is recorded as `runtime.unknown` — engine output is NOT our contract, it must not be able to break us | Strict parse: a minor engine release would break runs |

**Boundary rule (summary):** *our* contracts (env, API, DB) are validated strictly;
*other parties'* contracts (engine stream-json, Telegram) are read tolerantly + a raw copy is kept.

## Skeleton layout

The Go module lives **at the repo root** (`github.com/esmworks/OpenAssist`) — a root module
instead of a `core/` submodule: short import paths, golangci/CI from a single place, no `go.work`
needed. The Swift side lives under `mac/`, independent of the Go tooling. (A deliberate deviation
from the `core/` plan in roadmap Phase 0.)

```
cmd/core/           giriş: serve | migrate | healthcheck | version
internal/config/    tipli env yükleme + doğrulama
internal/httpapi/   özel API (:8080) — Faz 0'da yalnız /healthz
internal/store/     pgx havuzu + göç; internal/store/db = sqlc üretimi (elle DOKUNULMAZ)
db/migrations/      goose SQL göçleri (embed.FS ile binary'ye gömülü)
db/queries/         sqlc sorgu kaynakları
deploy/             Dockerfile + Dokploy compose
mac/                SwiftUI (Faz 6)
docs/design/        bu belge ve sonrakiler
```

`healthcheck` subcommand: the distroless image has no curl/wget; the compose healthcheck calls
the binary itself (PRD §4).

## Test strategy

- **Unit:** pure Go, no DB (`config`, `httpapi` with httptest, helpers such as `healthURL`).
- **Integration:** `-tags integration`, requires a real Postgres (`DATABASE_URL`); proves
  migrations + sqlc generation + roundtrip end to end. In CI, a `postgres:17` service container.
- **Generation freshness:** in CI, `sqlc generate && git diff --exit-code` — if generated code
  was edited by hand or is stale, the build goes red.
- **Lint:** golangci-lint (v2 config), the same locally and in CI.

## To revisit as it grows

- When the queue arrives in Phase 2: pgxpool sizing + whether the `FOR UPDATE SKIP LOCKED`
  queries can stay in sqlc (if dynamic ordering is needed, the escape hatch to raw pgx is open).
- If manual `Validate()` for API DTO validation gets tedious, go-playground/validator is added.
- The engine event vocabulary moves to its own package (`internal/engine/events`) (Phase 1) —
  the conformance tests live there with golden files (K18).
