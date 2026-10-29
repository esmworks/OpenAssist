-- +goose Up
-- ADIM 10 — grup sohbeti. İki boyut açılır:
--   bot_cursors + conversation_id: motor oturum zinciri artık (bot, motor,
--   konuşma) başına — grup üyesi botun kendi sohbeti ile grup sohbeti AYRI
--   zincirlerdir (ADIM 7 altyapısı, PRD Ek C.2).
--   runs + conversation_id: koşunun ait olduğu kanal; bot cevabı koşunun
--   kanalına (sohbetine) yazılır — grupta üye cevabı grup zaman tüneline düşer.
ALTER TABLE bot_cursors ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE CASCADE;
ALTER TABLE runs ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE SET NULL;

-- Backfill: mevcut imleçler botun kendi direct sohbetine aittir; koşular da.
UPDATE bot_cursors c
SET conversation_id = k.id
FROM conversations k
WHERE k.bot_id = c.bot_id AND k.kind = 'direct';

UPDATE runs r
SET conversation_id = k.id
FROM conversations k
WHERE k.bot_id = r.bot_id AND k.kind = 'direct';

-- Sohbeti yokken imleci olmuş satır (olamaz ama savunma) düşer; boşluk kalmaz.
DELETE FROM bot_cursors WHERE conversation_id IS NULL;

ALTER TABLE bot_cursors ALTER COLUMN conversation_id SET NOT NULL;
ALTER TABLE bot_cursors DROP CONSTRAINT bot_cursors_pkey;
ALTER TABLE bot_cursors ADD PRIMARY KEY (bot_id, engine, conversation_id);
CREATE INDEX runs_conversation_idx ON runs (conversation_id);

-- +goose Down
DROP INDEX runs_conversation_idx;
ALTER TABLE runs DROP COLUMN conversation_id;
ALTER TABLE bot_cursors DROP CONSTRAINT bot_cursors_pkey;
ALTER TABLE bot_cursors ADD PRIMARY KEY (bot_id, engine);
ALTER TABLE bot_cursors DROP COLUMN conversation_id;