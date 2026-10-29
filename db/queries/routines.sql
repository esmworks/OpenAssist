-- name: CreateRoutine :one
INSERT INTO routines (bot_id, name, trigger_kind, cron_expr, timezone,
                      input_source, expected_output, approval_note, stale_policy, next_run_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetRoutine :one
SELECT * FROM routines WHERE id = $1;

-- name: ListRoutines :many
SELECT * FROM routines ORDER BY created_at;

-- name: ListRoutinesByBot :many
SELECT * FROM routines WHERE bot_id = $1 ORDER BY created_at;

-- name: UpdateRoutine :one
UPDATE routines SET
    name = $2, cron_expr = $3, timezone = $4, input_source = $5,
    expected_output = $6, approval_note = $7, stale_policy = $8,
    next_run_at = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetRoutineEnabled :one
UPDATE routines SET enabled = $2, next_run_at = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteRoutine :execrows
DELETE FROM routines WHERE id = $1;

-- name: ClaimDueRoutines :many
-- Zamanlayıcı turu: vadesi gelen rutinleri KİLİTLEYEREK al (replicas:1 esas,
-- ama SKIP LOCKED iki kopya kazasında çift tetiklemeyi de önler — §4).
-- next_run_at ilerletilir; koşu enqueue edilemezse bir sonraki vadede tekrar denenir.
SELECT * FROM routines
WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= now()
ORDER BY next_run_at
LIMIT 10
FOR UPDATE SKIP LOCKED;

-- name: AdvanceRoutine :exec
UPDATE routines SET next_run_at = $2, last_run_at = now(), updated_at = now()
WHERE id = $1;

-- name: RecordRoutineRun :exec
INSERT INTO routine_runs (routine_id, run_id) VALUES ($1, $2);

-- name: PruneRoutineRuns :execrows
-- Rutin başına son 20 kayıt kalır (PRD §9 limitleri).
DELETE FROM routine_runs AS old
WHERE old.routine_id = $1 AND old.id NOT IN (
    SELECT rr.id FROM routine_runs rr
    WHERE rr.routine_id = $1 ORDER BY rr.id DESC LIMIT 20
);

-- name: ListRoutineRuns :many
SELECT rr.id AS routine_run_id, rr.created_at AS fired_at, r.*
FROM routine_runs rr
JOIN runs r ON r.id = rr.run_id
WHERE rr.routine_id = $1
ORDER BY rr.id DESC
LIMIT 20;
