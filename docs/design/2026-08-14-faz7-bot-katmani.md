# Phase 7 — Bot layer: router tools, memory, routine engine (2026-08-14)

Scope: K11 (skill/routine split + maturation path), K13 (setup is a conversation),
K14 (watcher schema), K12 (prepare-don't-send + stale data), PRD §8 memory model.

## 1. Management tools live in the MCP gateway — not a separate surface

`create_bot`, `create_routine`, `memory_save`… are not a separate "admin API"; they are
ordinary MCP tools the engine sees (`mcp__gw__*`, `internal/coretools`).
This is the core of K13: setup and roster management are a CONVERSATION, and the tools are
in the agent's hands. The gateway is now always on (in Phase 4 it was only enabled when
Google was configured) — core tools are unconditional, Google tools are added by
configuration.

## 2. Approval balance: light / approval-required split via allowed_tools

K13: creating/updating a bot is LIGHT; routine CRUD and deleting a bot REQUIRE APPROVAL.
The mechanism has two layers: (a) class declaration — all are write class (delete_bot
Ruleable=false: no rule can be written, it asks every time); (b) the router bot's
`allowed_tools` contains only `coretools.LightTools()` → light tools are
pre-authorized by the CLI and not asked about; routine CRUD, which is not on the list, falls
to the approval gate. So the "which tool asks" decision lives in the bot definition, not in code.
`pause_routine` is a deliberate exception: an operation in the safe direction, internal class.

## 3. Memory: the agent proposes, core persists

`memory_save` is a PROPOSAL channel, not a WRITE: core derives a sha256 hash from the
content, identical content in the same scope becomes an upsert (dedup), and lifetime
(`expires_in_days`) and confidence are carried along. Order of entry into the system prompt
per PRD §8: first `user` scope (all bots), then `bot` scope — the integration test also
verifies the order. Skills (K11 "how") are enabled per bot and go into the prompt's
"Enabled skills" section. A memory read error does not break the run:
a run without memory is better than a run that never happens.

## 4. Routine engine: advance the due time no matter what

Every 30 s the scheduler locks due routines with `ClaimDueRoutines` (FOR UPDATE SKIP LOCKED).
Critical order: `next_run_at` is advanced FIRST, the run is enqueued
afterwards — an enqueue error does not cause a trigger storm; it is retried at the
next due time. An unresolvable schedule (tz database changed, cron broken)
auto-pauses the routine and logs it. The timezone is in the DB;
`NextRun` is computed in the routine's own zone and written as UTC (§6 pitfall —
"Istanbul 07:00 = 04:00 UTC" is unit-tested). K11's mandatory test run
is covered by the `run_routine_now` tool; the tool's description and
`create_routine`'s return text remind the agent of this.

## 5. The night rhythm is NOT a new mechanism — its proof is new

Appendix A's "scan at night, wait for approval in the morning" rhythm is a combination of
Phase 3-5 mechanisms: quiet hours hold externals → park → approval_pending
(held) → morning approval → resume. Phase 7 only added the TRIGGER (routine) and
proved the whole thing in a single integration test
(`TestNightRoutineHeldThenMorningApproval`). `held` is not a separate state;
it is approval_pending itself (K12 first-class output state).

## 6. local_audit: runner calls are logged

Every Mac runner tool call lands in `local_audit` (device name, tool, arguments,
result — clipped to 2000 characters). The run correlation (run_id) is not known at the MCP
layer, so it is empty in v1; the correlation chain is noted for v1.5 (PRD §8).

## Files

- `db/migrations/00006_bot_katmani.sql` — memories, skills, bot_skills,
  routines, routine_runs, local_audit
- `internal/coretools` — 15 management tools
- `internal/routines` — NextRun/Validate/BuildPrompt + Engine (4 unit +
  3 integration tests)
- `internal/orchestrator.contextPrompt` — memory+skills → system prompt
- `internal/httpapi/routines.go` — routines/memories/skills/audit API
- `cmd/core` — gateway always on, seed bots, routine engine, audit hook
