# Phase 3 — Approval policy and security core

Date: 2026-08-13 · Status: implemented · Related: PRD K14/K17/K18, §5-6; roadmap Phase 3

## Decision 1 — The approval gate is in the orchestrator, not in the MCP gateway

The plan put the approval gate in the MCP gateway (K18-2). In the implementation the gate was
placed on the orchestrator's `onAsk` path: the Claude Code engine already asks us about **every**
permission via `--permission-prompt-tool mcp__oa__approve` — the flow is
proxy → Unix socket → broker → orchestrator. A second gate in the gateway would mean asking the
same question twice. Classification happens in one place (`policy.Classify`); in Phase 4 the MCP
gateway arrives as the connector *surface*, while the policy brain stays here.

## Decision 2 — Pure policy core (`internal/policy`)

`Classify` and `Decide` are pure functions: no DB, the clock is passed in.
This way every veto combination is proven with millisecond unit tests;
the integration tests only verify the wiring.

Veto order (first match wins):

1. **Deny rule** — always reject (tainted *strengthens* this, never relaxes it)
2. **Execute class** (Bash etc.) — no rule can be written, a human is always asked (K17)
3. **Tainted + side effect** — in a run that read external content, allow rules are void (§5.1)
4. **Daily quota** — `action_log` counter; if the counter can't be read, the quota counts as full (fail-closed)
5. **Quiet hours** — only the `external` class is held; local writes keep flowing at night too
6. **Allow rule** — applies only to `Ruleable` classes
7. If none of these → ask a human (short window → park)

## Decision 3 — The decision source travels in the event

`engine.Answer.Source` (`rule|api|grant|timeout|policy`) is written to the `request.resolved`
event. The activity screen answers "why was this allowed?" from the event; the quota counter is
also broken down by `action_log.decided_by`.

## Decision 4 — TTL and the `expired` state (K14)

If a parked approval is not answered within `ApprovalTTLHours` (default 72), the reaper moves
the run to `expired`. `expired` is terminal: the SSE stream closes, `ResolveApproval` rejects
(the DB `WHERE status='approval_pending'` also settles races). If the user wants, they repeat
the same prompt as a new run.

## Decision 5 — "Always allow" becomes a rule via two paths

- **Short path** (run is live): the `updatedPermissions` suggested by the CLI is returned as is
  (B.3 — rule syntax is never invented) **and** written to `approval_rules`.
- **Park path**: `POST /api/runs/{id}/approval` with `always: true` in the body → the rule is
  created on the server. The vetoes are on the server: if the class is not `Ruleable` or the
  run is tainted, no allow rule is written; a deny rule can always be written.

## Evidence

- Unit: `internal/policy` (classes, quiet hours across midnight, veto order),
  `internal/crypto` (AES-256-GCM round trip + tampering).
- Integration (real Postgres): rule auto-allow + `action_log`, general deny rule, tainted
  voiding an allow rule, `always` → rule → second run flows without asking, TTL → `expired` →
  approval rejected, webhook dedup.
