# Phase 8 — Cross-bot surfaces (K19) (2026-08-14)

Scope: Approval Center bulk approval, Activity feed, Tasks board. Principle:
NO new state machine — all three surfaces are human-readable combinations of existing
runs/routines/local_audit data.

## 1. "Approve all" is bounded by a four-layer veto

Approval is irreversible (K12 note: approval checks the proposed action), so
the limit is set up front. `POST /api/approvals/bulk` SKIPS the following:

1. **Questions** — they need an answer; they cannot be parked anyway (Phase 5), the veto
   is a defensive layer.
2. **Tainted runs** — external content was read; §5.1 requires one-by-one review.
3. **Class vetoes** — execute and Ruleable=false (delete_bot) tools.
4. **Name patterns** — tool names containing delete/trash/remove/purchase/buy/pay/sil;
   a last line of defense even if the class declaration is wrong.

Every skipped request is reported with its reason; the Swift summary line shows "N approved ·
M skipped (review one by one)". Approved ones go through the normal ResolveApproval
path (including resume) — bulk approval is NOT a separate fast path.

## 2. Activity: two sources, one feed

`GET /api/activity` merges runs (all states) and `local_audit` rows into a single
timeline. Raw run_events data is deliberately left out:
Activity is a human-readable summary; event-level detail stays in the run detail (SSE).

## 3. Tasks board: a query union

`GET /api/tasks` returns three state lists (running/queued/approval_pending) + a routine
showcase. A routine row carries its next run time — the answer to "what will the bot do
and when" on a single screen.

## 4. Go to chat from anywhere

K19's connective tissue: from an approval card, activity row, task row and routine
row, one click takes you to the relevant bot's chat (Swift `model.select`).

## Files

- `internal/httpapi/surfaces.go` — bulk/tasks/activity endpoints (+
  `surfaces_integration_test.go`: veto proof)
- `internal/orchestrator.ClassifyTool` — class lookup exposed to UI surfaces
- `db/queries/runs.sql` — ListRecentRuns
- Swift: ActivityView (feed) + TasksView (board) + Inbox bulk approve button
