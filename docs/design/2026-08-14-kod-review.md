# Code review — after v1 close-out (2026-08-14)

Scope: cumulative work of Phases 6–10 (runner bridge, bot layer, surfaces,
deployment, E2E) + cross-checks of the core. Method: skeptical area-by-area
reading — a hunt for concrete bugs such as reconnection races, UTF-8 boundaries, image contents,
token lifecycle, the throttle's last-edit guarantee, offset semantics. Style
nits are out of scope.

## Bugs found and fixed

1. **Runner hub reconnection race** (`internal/runner/hub.go`):
   the `pending` map was SINGLE per hub and `unregister` closed all of it.
   When the Mac woke up and reconnected, the delayed close of the old connection
   could drop a pending call that had gone to the NEW connection with
   `ErrNotConnected` (while the runner was healthy and the answer was on its way). Fix: `pending`
   moved to the session; `unregister` closes only its own session's
   calls. Regression test: `TestUnregisterClosesOnlyOwnPending`.

2. **Byte-cutting in the audit record** (`cmd/core/main.go` `clip`): the 2000
   byte limit could cut in the middle of a rune → invalid UTF-8 →
   Postgres 22021 rejection → `local_audit` row SILENTLY lost (the error was
   already being swallowed). Fix: the cut is pulled back to a rune boundary. Test:
   `TestClipKeepsValidUTF8`.

3. **No CA bundle in the image** (`deploy/Dockerfile`): `node:22-slim` does not ship a CA
   certificate bundle (Node uses its own embedded roots —
   which is why the engine works and the problem stays hidden). The Go core's outbound HTTPS
   (Telegram Bot API, Google OAuth token endpoint) would fail in the production container
   with `x509: certificate signed by unknown authority`.
   Fix: `ca-certificates` installed in the runtime layer; the image was rebuilt
   and the bundle verified inside it. (tzdata IS IN THE IMAGE — verified separately for routine
   time zones; no need for the `time/tzdata` embed.)

## Areas verified clean

- Telegram: callback tokens are single-use (`UseApprovalToken`
  has a single winner in a race), allowlist on callbacks too, `answerCallbackQuery`
  on all paths; the 4096 limit is clipped rune-safely at the API layer;
  the EditEvery throttle always lets the last edit through (turn.completed →
  `flush(true)` + finishRun); `streamRun` goroutines are tied to drain via
  ctx/wg.
- Orchestrator: `ResolveApproval` is atomic on double calls (in-process hub
  has a single winner; conditional UPDATE on the park path — the loser gets `ErrNotAwaiting`);
  which is also why the bulk approve race is safe.
- Policy: quiet hours crossing midnight and `start==end`, quota day across
  DST transitions (`time.Date` normalization), fail-closed quota counter.
- httpapi `clipText` is already rune-safe; `bulkVetoed` name patterns are
  lowercase-contains ("sil" also catches Turkish names).
- crypto: Seal uses a fresh random nonce on every call, correct GCM usage.

## Accepted limits (deliberate, not fixed)

- Telegram getUpdates: in-process at-most-once (offset advances before
  processing), at-least-once on restart (offset is in memory) — with a single
  user a duplicate run is rare and harmless; persistent offset in v1.5.
- Memory/skill content goes into the system prompt (by design) —
  injection risk is bounded by the approval gate (side effects are asked about).
- The v1 limits from the Phase 10 security review still apply
  (run-wide taint, quota race, MASTER_KEY in env).
