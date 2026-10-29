# Phase 1 spike notes — Claude Code integration proof

Date: 2026-08-13 · Environment: macOS, claude CLI 2.1.226 · Result: **3/3 scenarios passed**

Code: `internal/engine` (K18 contract) + `internal/engine/claudecode` (adapter) +
`cmd/oa-mcp-proxy` (permission proxy) + `cmd/spike` (scenarios). Run: `make spike`.

## Working invocation

```
claude -p --output-format stream-json --input-format stream-json \
  --verbose --include-partial-messages \
  --permission-mode default \
  --permission-prompt-tool mcp__oa__approve \
  --mcp-config '{"mcpServers":{"oa":{"command":"<bin>/oa-mcp-proxy","args":["<soket>"]}}}' \
  [--allowedTools …] [--model …] [--resume <session_id>]
```

The prompt goes in via stdin as a stream-json message; the env is built from scratch
(`cleanEnv`), and the process runs in its own group (`Setpgid` + `kill(-pid)`).

### Identity: dropping `USER` makes `claude login` invisible (measured 2026-08-14)

`claude login` stores the identity in the macOS keychain under the
`Claude Code-credentials` service, with the **account field set to the username**
(`"acct"<blob>="<username>"`). The CLI looks the entry up by `$USER`. Because `cleanEnv`
originally dropped `USER`, the engine could not find the identity even on a logged-in
machine. Single-variable bisect in the same environment:

| `cleanEnv` + | result |
|---|---|
| — | `Not logged in · Please run /login` |
| `__CF_USER_TEXT_ENCODING` / `XPC_SERVICE_NAME` / `SSH_AUTH_SOCK` / `LOGNAME` | `Not logged in` |
| **`USER`** | **`OK`** — run passed |
| `CLAUDE_CODE_OAUTH_TOKEN` (fake) | `401 OAuth access token is invalid` |

First three rows: on a local Mac the fix is to allowlist `USER`; no other identity
variable is needed. The last row is a separate finding: the env token is also a valid
identity channel — since there is no keychain in the container,
`CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`) is needed there. If neither is present
and there is no keychain either, a warning is shown at startup.

## Scenario 1 — basic (stream + cost)

`session.started` → delta stream → `turn.completed{ok, sebep, maliyet, token}`.
Result: ok=true, cost=$0.2108, cursor (session_id) obtained. ✓

## Scenario 2 — park/resume (the spike's main proof) ✓

1. **Turn 1:** Write → `request.opened{permission}` → 3 s window expired →
   `deny(DenyParkNote)` → tool_result **error=true** → the model politely SUMMARIZED
   it as "parked while awaiting approval" and **finished the run successfully**
   (`ok=true`) — the park note copy works exactly as intended: the run does not break,
   it completes. The file was NOT written (the gate held).
2. **Turn 2:** `--resume <imleç>` → the model retried the same Write on its own →
   broker allow → file written, content verified byte for byte.

Measurements: resume continued **with the same session_id**; turn 2 input was only
**526 tokens** (context is carried efficiently); turn 2 cost $0.0476.

## Scenario 3 — ask_user (question as a first-class request) ✓

`mcp__oa__ask_user` (pre-allowed in AllowedTools) → `request.opened{question}` →
broker "turkuaz" → final text "Rengin: turkuaz" ("Your color: turquoise").
Question/permission separation works end to end.

## Field lessons (addendum to Appendix B)

- **User settings can override the model flag:** the local `~/.claude` configuration
  (third-party endpoint) opened the session with `glm-5.2`; `--model haiku` had no effect.
  The adapter didn't care — the vocabulary is engine-agnostic (K18 validated). Not an
  issue on the server, since the config there will be under our control; but **the
  cost/model report must be read from the `session.started`/`result` event, not from
  the flag** (which is what we do).
- **An approval denial flows as `tool_result is_error=true`**; the run ends not with an
  error but with the model's summary (`turn.completed ok=true`). So in the parked state
  the user should be shown "awaiting approval", not "run broke" — the state machine will
  derive `runs.status=approval_pending` from the broker decision, not from the result event.
- On resume the permission request comes again (one more `approve` call) — this is
  exactly where the "single-use scoped grant" is issued; no separate grant API is
  needed.
- `--mcp-config` works fine as an inline JSON argument; the socket path was kept short
  so it doesn't hit the macOS 104-byte limit (`…/oa-broker-*/b.sock`).

## Work carried over to Phase 2

The adapter is now real: the run state machine (including `approval_pending`),
`run_events` persistence + NDJSON tee, the queue and SSE will sit on top of it. The
conformance test's golden event sequences will be derived from these spike outputs.
