# Phase 10 — Security review (2026-08-14)

Scope (roadmap Phase 10): taint, quota, rule table, token encryption. Method:
code reading + mapping of existing test evidence + an E2E scenario. One
real bug found in this pass was fixed (§6).

## 1. Taint (§5.1) — VERIFIED

- Marking: when the `orchestrator.go` event stream sees a tool use classed `DataSource=true`,
  it calls `SetRunTainted` + an in-process `atomic.Bool` (takes effect
  immediately within the run without going back to the DB).
- Enforcement: `policy.Decide` veto order (K17)
  `deny→execute→tainted→kota→sessiz saat→allow→sor` — in a tainted run,
  side-effecting classes CANNOT SEE an allow rule (vetoed before the rule check).
  The deny rule comes even before tainted: taint cannot weaken a deny.
- Persistence veto: an "always allow" approval in a tainted run is not turned into a rule
  (`ResolveApproval` `!cls.Ruleable || run.Tainted` check), and bulk approve
  skips tainted runs (Phase 8, `surfaces.go`).
- Test evidence: `policy_test.go` decision tables + orchestrator integration
  tests + Phase 8 `TestBulkApproveLimits` (tainted is skipped).
- Limit (deliberate, PRD §5.1): taint is a single run-wide flag; which content was
  carried is not tracked (data-flow analysis is out of v1).

## 2. Quota — VERIFIED

- Counter: `action_log` counted via `CountActionsSince`; the day starts at local
  midnight in the policy's time zone (`Settings.DayStart`).
- Only side-effecting classes are counted (`logAction` does not write read/internal);
  automatic permissions (rule/grant/api) are all logged → the quota also limits
  the automatic flow (K17: when exceeded, every action is asked of a human; work does not stop).
- Fail-closed: if the counter cannot be read, the quota is treated as full (`countActionsToday`).
- Limit: the quota check and the action log are not a single transaction — two concurrent
  runs can exceed the limit by 1. Acceptable on a single-user system; the limit
  falls back to "ask", it does not block (soft-limit design).

## 3. Rule table (K17) — VERIFIED

- Read: `lookupRule` bot-specific rule; error → nil → safe side: ask.
- Ruleable vetoes at every layer: decision (`Decide` applies an allow rule only if
  `cls.Ruleable`), rule creation (`AllowAlwaysRule` only when
  Ruleable + untainted; never execute), API bulk approve (execute or
  non-Ruleable tools are skipped), Telegram "Always allow" (same vetoes in
  ResolveApproval).
- A deny rule is always applied regardless of Ruleable (denying is always
  safe).
- An unknown tool class is `ClassExternal` (does not run without asking) — the Classify
  heuristic falls to the safe side; the gateway explicitly declares the class of its own tools
  (Phase 4), Mac tools are fixed in `runner.StaticClasses`.

## 4. Token encryption — VERIFIED

- Encryption at rest: AES-256-GCM, `nonce||ciphertext` base64
  (`crypto/seal.go`); key `MASTER_KEY` (64 hex), config validation
  makes the key MANDATORY if the Google connector is enabled (`config.Validate`).
- The Google refresh token is stored only sealed (`SealedRefreshToken`);
  an unseal error surfaces to the user as "did MASTER_KEY change?". Test:
  `TestConnectSealsAndStores` (plaintext never reaches the DB).
- Telegram callback tokens: random, single-use, kept on the server;
  `callback_data` carries only the opaque token, and old ones are deleted each time
  the card is redrawn (`drawCard`). The answer text never goes out to Telegram.
- The Telegram bot token comes from env and is not written to the DB.
- Limit: `MASTER_KEY` lives as an env variable (Dokploy secret) — KMS/keyring is out
  of v1; accepted for a single-server setup.

## 5. Peripheral checks (out of scope but looked at)

- Telegram gateway is fail-closed: messages from a chat not on the allowlist
  are not processed; callbacks also pass the chat allowlist check (`gateway.go`).
- Mac runner path safety: `LocalPolicy.canonicalize` closes the intermediate-symlink escape
  (a real bug fix proven by a test in Phase 6); no permanent
  deletion, only moving to the trash (K security).
- Questions never park (Phase 5): timeout → the bot makes its own decision;
  `approval_pending` holds only permission requests.

## 6. Bug found and fixed in this pass

`runner.Hub.Call` audit hook: because defers run LIFO, the call's
`cancel()` ran BEFORE the audit defer and the hook received a CANCELED
context → `RecordLocalAudit` silently failed with `context canceled`, and
local_audit was NEVER written. The E2E scenario test caught it. Fix:
the hook takes `context.WithoutCancel(ctx)` — a timeout is exactly the outcome that
must be recorded. Evidence: `TestV1EndToEnd` verifies the local_audit row.

## Still open (post-v1 / user step)

- The HTTP API has no authentication — by design (PRD §4: protection via Tailscale or
  in front of Dokploy; the public surface is the separate `publichttp`). Verifying in the
  deployment that 8080 is closed to the outside is a user step.
- The race in the quota check (§2) and run-wide taint granularity (§1) are
  deliberate v1 limits; consistent with the PRD.
