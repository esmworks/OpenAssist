# Phase 2 design record — Core: queue, run state machine, API

Date: 2026-08-13 · Status: accepted · Related: PRD K6/K15/K18/K19, Appendix B, design package (design_handoff_openassist)

## Alignment with the design package

The Claude Design package (`~/Downloads/design_handoff_openassist`) is the hi-fi reference for
the SwiftUI client; the server contract was set by the README's "what must come from the server"
list + the state vocabulary in the prototype:

- Task states `running / queued / waiting / done / error` ↔ run states
  `running / queued / approval_pending / completed / failed` (+ `canceled`).
- **Teams entered the data model now** (`bots.kind='team'` + `team_members`) —
  they are first-class in the design; multi-bot orchestration (team chat) stays in v2
  (PRD §10), and for now the API returns 422 for team chat.
- The handoff README assumes "api + worker as two services"; **we stay a single service**
  (K6: the queue is in Postgres, the workers in the same process). If scale is needed, a second
  core instance is started in compose — the queue is already multi-instance safe thanks to
  `FOR UPDATE SKIP LOCKED`, and the `pg_notify` broadcast also works across instances.

## Run state machine

```
queued → running → completed | failed | approval_pending
approval_pending → (karar: allow/deny) → queued (resume, aynı satır) → running → …
running (kalp atışı kesildi) → queued (reaper, attempt<max) | failed
```

- **Short path:** when a permission request arrives, the run opens an `APPROVAL_WAIT_SECONDS`
  (60 s) window; if the decision arrives from the API in time, it continues in-process (the
  broker path from the spike).
- **Park:** if the window runs out, deny (park note) → the engine politely summarizes and
  finishes → the row is `approval_pending`, the request is in `pending_ask`, the cursor in
  `result_cursor`.
- **Resume:** the decision re-queues the same row with a new prompt + `grant_decision`;
  the engine asks for the same permission again, and the orchestrator consumes the single-use
  grant. Spike lesson (spike-notlari.md): `approval_pending` is derived from the broker
  decision, not from the result event — the engine finishes with `ok=true` even when parked.
- First answer wins: the in-process race is settled under a single lock in `approvalHub`, the
  park race in the DB with `UPDATE … WHERE status='approval_pending'`.

## Persistence decisions

- **content.delta is not persisted** — the token stream goes only to the live bus/SSE; in the DB
  the full text lives in `turn.completed` + the `messages` row. A client that disconnects gets
  the persisted events via Last-Event-ID; losing intermediate deltas is accepted.
- Event tee: `DATA_DIR/run-events/<run>.ndjson` — a raw copy, for debugging
  (Appendix B.2-5). A write error does not break the stream.
- `cost_usd` is double precision: the cost is the engine's estimate anyway; numeric/pgtype isn't
  worth the ergonomic cost. If billing precision is ever needed, a migration is cheap.
- Bot workspace: `DATA_DIR/workspaces/<bot_id>` — runs happen in an isolated CWD per bot
  (the foundation of the "Files" surface for bots that produce files).

## SSE contract

`GET /api/runs/{id}/events` — `text/event-stream`; `id:` = `run_events.id`,
`event:` = event type, `data:` = payload JSON. Events after `Last-Event-ID` (or `?after=`)
are backfilled; the subscription is made BEFORE the backfill, duplicates are dropped by id.
`approval_pending` does not close the stream (resume continues in the same run);
after `completed/failed/canceled` comes `stream.end`. A `: ping` every 25 s.

## Test strategy

The orchestrator receives the engine through an interface → in CI the full loop is proven with
`enginetest` fakes (no claude needed): completion, park→resume, short path,
shadow-engine rule. The real CLI proof keeps living in `make spike`.

## Left for Phase 3+

- Approval rules (K17 table), the tainted flag, recording the channel source (`source`).
- Parking questions — for now the window + "make your own decision".
- An API endpoint for the `canceled` state; run cancellation (kill).
- Routines/scheduler (Phase 7), activity and statistics endpoints (K19 views).
