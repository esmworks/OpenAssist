-- name: InsertRunEvent :one
INSERT INTO run_events (run_id, kind, payload)
VALUES ($1, $2, $3)
RETURNING id;

-- name: ListRunEventsAfter :many
-- SSE geri doldurma: Last-Event-ID sonrasını sırayla verir.
SELECT * FROM run_events WHERE run_id = $1 AND id > $2 ORDER BY id LIMIT 1000;
