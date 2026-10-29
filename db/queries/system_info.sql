-- name: GetSystemInfo :one
SELECT * FROM system_info WHERE key = $1;

-- name: SetSystemInfo :exec
INSERT INTO system_info (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
