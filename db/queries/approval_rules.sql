-- name: UpsertApprovalRule :one
INSERT INTO approval_rules (bot_id, tool, action, note, created_by_run)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (bot_id, tool) DO UPDATE SET action = EXCLUDED.action, note = EXCLUDED.note
RETURNING *;

-- name: GetRuleForTool :one
-- Bot'a özel kural genel kuralı ezer.
SELECT * FROM approval_rules
WHERE tool = sqlc.arg(tool) AND (bot_id = sqlc.arg(bot_id) OR bot_id IS NULL)
ORDER BY bot_id NULLS LAST
LIMIT 1;

-- name: ListApprovalRules :many
SELECT * FROM approval_rules ORDER BY created_at DESC;

-- name: DeleteApprovalRule :execrows
DELETE FROM approval_rules WHERE id = $1;
