-- name: LogAction :exec
INSERT INTO action_log (bot_id, run_id, tool, class, decided_by)
VALUES ($1, $2, $3, $4, $5);

-- name: CountActionsSince :one
SELECT count(*) FROM action_log WHERE bot_id = $1 AND created_at >= $2;

-- name: ListActions :many
SELECT * FROM action_log ORDER BY created_at DESC LIMIT $1;
