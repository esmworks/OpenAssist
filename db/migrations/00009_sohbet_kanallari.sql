-- +goose Up
-- ADIM 4 (karşılaştırma planı): sohbet kanalları — PRD §8'in hep planladığı
-- ama v1'de açılmayan tablo. Dallanma, botlar arası devir ve grup sohbetinin
-- ortak önkoşulu.
--
-- Model değişmezi (K10): bot başına TEK sohbet (unique index). direct =
-- botla 1:1 sohbet; group = ekip satırının (bots.kind='team') taşıdığı çok
-- botlu sohbet — ekibin members'ı team_members'tan gelir (ADIM 10).
CREATE TABLE conversations (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       text        NOT NULL DEFAULT 'direct' CHECK (kind IN ('direct','group')),
    -- direct'te sohbetin sahibi bot; group'ta sohbeti taşıyan ekip satırı.
    bot_id     uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    title      text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX conversations_bot_idx ON conversations (bot_id);

ALTER TABLE messages ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE CASCADE;

-- Backfill: her bot için sohbet aç, mevcut mesajları bağla. Ekip botları
-- v1'de 422 ile reddedilir; yine de grup sohbetleri bugünden hazır dursun
-- diye kind='group' ile doğar (başlık = ekip adı).
INSERT INTO conversations (bot_id, kind, title)
SELECT id,
       CASE WHEN kind = 'team' THEN 'group' ELSE 'direct' END,
       CASE WHEN kind = 'team' THEN name ELSE '' END
FROM bots;

UPDATE messages
SET conversation_id = c.id
FROM conversations c
WHERE c.bot_id = messages.bot_id;

ALTER TABLE messages ALTER COLUMN conversation_id SET NOT NULL;

-- +goose Down
ALTER TABLE messages DROP COLUMN conversation_id;
DROP TABLE conversations;