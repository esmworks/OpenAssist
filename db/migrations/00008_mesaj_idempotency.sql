-- +goose Up
-- ADIM 3 (karşılaştırma planı): aynı mantıksal gönderimin tekrarı tek koşu
-- üretsin. Anahtar istemci üretir (retry'de aynı anahtar gider); kısmi index
-- sayesinde anahtarsız (kayıtsız) mesajlar hiç etkilenmez. Kapsam bot+kanal:
-- aynı anahtar farklı botta farklı gönderimdir.
ALTER TABLE messages ADD COLUMN idempotency_key text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX messages_idempotency_idx
    ON messages (bot_id, origin, idempotency_key)
    WHERE idempotency_key <> '';

-- +goose Down
DROP INDEX messages_idempotency_idx;
ALTER TABLE messages DROP COLUMN idempotency_key;