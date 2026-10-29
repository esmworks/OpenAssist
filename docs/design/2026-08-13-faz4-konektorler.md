# Phase 4 — Connectors: MCP gateway + Google OAuth

Date: 2026-08-13 · Status: implemented · Related: PRD K15/K17/K18, §5; roadmap Phase 4

## Decision 1 — Gateway inside core, `/mcp` on the private surface

The MCP Streamable HTTP server is not a separate process but a handler mounted on core's
private HTTP surface (`:8080/mcp`), living in `internal/gateway`. The engine (claude CLI) runs
on the same machine as core (K15) → the connection is `http://127.0.0.1:<port>/mcp`
and is added to `--mcp-config` as `"gw": {"type":"http","url":…}`. Tool names are
`mcp__gw__*`. Listening on a separate process/port would complicate deployment; the private
surface is already closed to the outside.

The protocol is minimal and deliberately narrow: `initialize` (version negotiation), `ping`,
`tools/list`, `tools/call`; no GET stream (405). Our tools are short-lived and return a single
result, so there is no need for SSE mode.

## Decision 2 — Class declarations are explicit; heuristics only for the unknown

The Phase 3 `policy.Classify` heuristic would have counted `mcp__gw__gmail_search` as external
(the last segment "gmail_…" doesn't start with a read prefix). The gateway EXPLICITLY declares
each tool's class at registration; `policy.ClassifyWith(overrides, tool)` checks the
declaration first. The orchestrator carries the map via `Config.ToolClasses` — the policy
package stays pure. Integration proof: the run gets tainted because the declaration says
`DataSource` (the heuristic would not have tainted it).

Class rationale:

| Tool | Class | Why |
|---|---|---|
| gmail_search / gmail_read / calendar_list_events | read + DataSource | reads external content → taint (§5.1) |
| gmail_create_draft | write | NOT outward-facing; preparing drafts at night must not be caught by the quiet-hours veto (Appendix A rhythm) |
| gmail_send_draft / calendar_create_event | external | sends a message/invite → approval gate + quiet hours |

## Decision 3 — Draft-first flow

There is NO direct "send" tool for Gmail: `gmail_create_draft` → (approval) →
`gmail_send_draft`. What gets sent is always a concrete draft sitting in Gmail, which the user
can see and edit in the Approval Center. The v1 bot's nightly rhythm runs on this pair.

## Decision 4 — Token storage and refresh

- The refresh token is sealed with AES-256-GCM and written to the `connectors` table
  (`MASTER_KEY`, Phase 3 `internal/crypto`). A plaintext token is stored nowhere;
  the API DTO never carries the sealed field.
- The access token lives only in an in-process cache (`oauth2.ReuseTokenSource`).
- `invalid_grant` (the user revoked access) is a permanent error: the connector is moved to
  `status=error`, and the tools return a "reconnect" error.
- Consent asks for `access_type=offline&prompt=consent` on every connect — Google only issues
  a refresh token on the first consent, and prompt=consent forces it every time.
- Scopes: `gmail.modify` (does NOT INCLUDE permanent deletion — K security),
  `calendar.events`, `userinfo.email`.

## Decision 5 — Handoff card = MCP isError result

When the connector is missing/disconnected, a tool call returns not a JSON-RPC error but a
result with `isError:true` content. The model reads the error and explains it to the user in
its own words ("Google isn't connected, connect it at this address…"). We did not add a
separate health probe at run start: validation already happens at tool-call time and the bot
does the handoff in natural language; `GET /api/connectors` gives the status to the UI.

## Decision 6 — OAuth surface and CSRF

`/oauth/google/start` → 302 consent; the state is 32 random bytes, single-use, 10 min TTL,
in memory (single process in v1). `/callback` consumes the state, exchanges the code for a
token, names the account via `userinfo.email`, seals and upserts. The redirect URI is
`PUBLIC_BASE_URL + /oauth/google/callback` — it must match the registration in the Console
exactly.

## Boundary rule reminder

Google APIs are a foreign contract: responses are decoded tolerantly. Our tool schemas are
strict: `additionalProperties:false` + `DisallowUnknownFields`.

## Evidence

- Unit: gateway protocol (initialize/list/call/isError/405/-32601), ClassifyWith precedence,
  OAuth happy path + forged state + consent denial + single-use state, token sealing (no
  plaintext leaks), cached refresh (a single token request), invalid_grant → error state,
  RFC2822 draft construction (UTF-8 subject/body), calendar list/create — all against a fake
  Google server.
- Integration (real Postgres): the gateway class declaration taints the run.
