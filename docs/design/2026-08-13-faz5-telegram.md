# Phase 5 — Telegram gateway

Date: 2026-08-13 · Status: implemented · Related: PRD K16, K14; roadmap Phase 5

## Decision 1 — Telegram is a channel, not a second system

An incoming message enters the same queue via `EnqueueChat`; the distinction is a single field:
`messages.origin` and `runs.origin` (`api | telegram | mac | routine`).
`runs.origin` was needed because a parked run may resume hours later — which channel to notify
of the result is told by the row, not by an in-memory mapping.

## Decision 2 — The card does not wait for the park

The first design opened the card in the `approval_pending` state. Wrong: questions are NOT
parked (when the window runs out the bot makes its own decision, Phase 1), and waiting for the
park for permissions too would defeat the purpose of K16 (fast approval with a single tap).
Now the card opens on the `request.opened` event — if the user taps within the short wait
window, the run continues WITHOUT parking at all (short path). If no answer comes, the run is
parked and the card is redrawn in `finishRun` with fresh tokens.

On the `request.resolved` event the card closes — except when `source=timeout` (park): the user
must still be able to tap hours later.

## Decision 3 — The button ID is unguessable and single-use

`callback_data` = 24 random bytes (base64url, ~32 characters; the Bot API limit is 64 bytes).
The `approval_tokens` row is consumed atomically with `UPDATE … WHERE used_at IS NULL`. When
the card is redrawn, the old tokens are deleted. So there is neither a predictable id
(`approve_183`) nor a reusable button.

## Decision 4 — The multi-channel race goes through a single gate

Whichever channel a decision comes from, it goes through `Orchestrator.ResolveApproval`
(atomic: the in-process hub or `UPDATE … WHERE status='approval_pending'`).
The new `Config.OnApprovalResolved` hook announces the decision to the channels; the Telegram
card closes and says "answered from another channel". A late tap is handled in two places: if
the token was already consumed, "button is not valid"; if the run is no longer waiting,
"already handled".

## Decision 5 — Streaming: a single message, edited in place

For each run one message is opened ("⏳ thinking…") and updated with `editMessageText`. Two
pitfalls were closed:

- **Race:** the worker may finish the run before the bus subscription. When `streamRun`
  starts it polls the run state once (the counterpart of the subscribe→backfill pattern in
  SSE) and carries a 5 s safety-net ticker (the bus drops events for a slow client).
- **`turn.completed` is faster than the DB:** the event is published before the run state is
  written. `awaitSettled` briefly waits for the state to settle; if it doesn't make it, the
  text accumulated in the stream is used as a fallback.

## Decision 6 — The allowlist is fail-closed, the privacy cost is explicit

A chat with no row in `telegram_chats` is unauthorized: it doesn't even get a reply
(we don't leak our existence). If a token is set while `TELEGRAM_ALLOWED_CHAT_IDS` is empty,
startup fails. The risk accepted in K16 still applies as is: the Bot API is not end-to-end
encrypted, and the channel is optional.

## Test isolation note (CI)

Integration tests now run SEQUENTIALLY with `-p 1`. All packages share the same database and
the same run queue (`FOR UPDATE SKIP LOCKED`); when run in parallel, one package's worker was
grabbing another package's run and applying its own fake engine. This is the cost of the tests
sharing the real queue — we chose running sequentially over faking the queue behavior.

## Evidence

- Unit (fake Bot API): getUpdates/sendMessage bodies, swallowing "message is not modified",
  long-text truncation, API errors carrying the description.
- Integration (real Postgres + fake engine + fake Bot API): chat flow and channel stamp, silent
  rejection of an unauthorized chat, approval card + single-use token, short-path approval
  (no park), question option buttons, closing a card resolved from another channel.
