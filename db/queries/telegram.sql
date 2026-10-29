-- name: GetTelegramChat :one
-- Satır yoksa sohbet izinli değildir (fail-closed allowlist).
SELECT * FROM telegram_chats WHERE chat_id = $1;

-- name: UpsertTelegramChat :one
INSERT INTO telegram_chats (chat_id, label)
VALUES ($1, $2)
ON CONFLICT (chat_id) DO UPDATE SET label = EXCLUDED.label, updated_at = now()
RETURNING *;

-- name: SetTelegramActiveBot :exec
UPDATE telegram_chats SET active_bot_id = $2, updated_at = now() WHERE chat_id = $1;

-- name: ListTelegramChats :many
SELECT * FROM telegram_chats ORDER BY created_at;

-- name: DeleteTelegramChat :execrows
DELETE FROM telegram_chats WHERE chat_id = $1;

-- name: CreateApprovalToken :exec
INSERT INTO approval_tokens (token, run_id, action, answer) VALUES ($1, $2, $3, $4);

-- name: UseApprovalToken :one
-- Tek kullanım: yalnız kullanılmamış token satır döndürür (atomik).
UPDATE approval_tokens SET used_at = now()
WHERE token = $1 AND used_at IS NULL
RETURNING *;

-- name: DeleteApprovalTokensForRun :exec
DELETE FROM approval_tokens WHERE run_id = $1;

-- name: UpsertTelegramCard :exec
INSERT INTO telegram_cards (run_id, chat_id, message_id)
VALUES ($1, $2, $3)
ON CONFLICT (run_id) DO UPDATE SET
    chat_id = EXCLUDED.chat_id, message_id = EXCLUDED.message_id, resolved = false;

-- name: GetTelegramCard :one
SELECT * FROM telegram_cards WHERE run_id = $1;

-- name: ResolveTelegramCard :one
-- Kartı kapatır; yalnız açık kartta satır döner (ikinci tıklama boş alır).
UPDATE telegram_cards SET resolved = true
WHERE run_id = $1 AND resolved = false
RETURNING *;
