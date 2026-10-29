-- name: RecordLocalAudit :exec
INSERT INTO local_audit (run_id, device, tool, args, ok, result)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListLocalAudit :many
SELECT * FROM local_audit ORDER BY id DESC LIMIT $1;
