-- name: UpsertBotConversation :one
-- Botun sohbetini dondurur — yoksa açar (ADIM 4: sohbet ilk kullanımda doğar;
-- bot başına tek sohbeti conversations_bot_idx garantiler). Çakışmada sıfır
-- satır döner: çağıran GetBotConversation'a düşer.
INSERT INTO conversations (bot_id, kind, title)
VALUES ($1, $2, $3)
ON CONFLICT (bot_id) DO NOTHING
RETURNING *;

-- name: GetBotConversation :one
SELECT * FROM conversations WHERE bot_id = $1;
-- name: GetConversation :one
-- Konuşmayı id ile getirir (ADIM 10 ikinci aşama): koşu hangi kanalda
-- koştuğunu conversation_id ile bilir, kanalın grup olup olmadığını buradan
-- öğrenir (grup ise bot_id ekip satırıdır).
SELECT * FROM conversations WHERE id = $1;
