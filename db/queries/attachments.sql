-- name: CreateAttachment :one
INSERT INTO attachments (bot_id, kind, name, size, path)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAttachment :one
SELECT * FROM attachments WHERE id = $1;

-- name: SelectAttachmentsByIDs :many
SELECT * FROM attachments WHERE id = ANY($1::uuid[]) ORDER BY created_at;

-- name: ListAttachmentsByMessages :many
SELECT * FROM attachments WHERE message_id = ANY($1::uuid[]) ORDER BY created_at;

-- name: BindAttachments :execrows
-- upload→send akışının son adımı: ekleri mesaja bağlar. Yalnız henüz
-- bağlanmamış satırları sayar; eksik/başka mesajın olan eklerde satır sayısı
-- kısa gelir — SaveChat bunu görüp transaction'ı geri sarar.
UPDATE attachments SET message_id = $2
WHERE id = ANY($1::uuid[]) AND message_id IS NULL;

-- name: CreateRunAttachment :one
-- Botun ÜRETTİĞİ ek (ADIM 13): koşuya bağlı doğar, mesaj yazılınca mesaja
-- bağlanır. bot_id sahibi, run_id üreteni söyler.
INSERT INTO attachments (bot_id, run_id, kind, name, size, path)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAttachmentsByRun :many
-- Koşunun giden ekleri (bağlanmış ya da bağlanmamış): Telegram dosyayı
-- buradan bulup gönderir.
SELECT * FROM attachments WHERE run_id = $1 ORDER BY created_at;

-- name: CountRunAttachments :one
-- Kaçak döngü kapısı: bir koşu sınırsız dosya üretemez (send_file her
-- çağrıda bunu okur).
SELECT count(*) FROM attachments WHERE run_id = $1;

-- name: BindRunAttachments :execrows
-- Koşunun ürettiği ekleri botun mesajına bağlar. Yalnız bağlanmamış satırlar:
-- mesaj iki kez yazılsa bile ek ilk mesajda kalır.
UPDATE attachments SET message_id = $2
WHERE run_id = $1 AND message_id IS NULL;

-- name: ListAttachmentPathsByConversation :many
-- Sohbet temizliğinde disk dosyaları da silinebilsin diye yolları önden topla
-- (mesajlar silinince ek satırları CASCADE ile düşer, yollara ulaşım kalmaz).
SELECT a.path
FROM attachments a
JOIN messages m ON m.id = a.message_id
WHERE m.conversation_id = $1;
