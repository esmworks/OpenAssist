-- +goose Up
-- ADIM 7 — motor→imleç eşlemesi (Ek C.2, v1.5 maddesi): imleç motora özeldir
-- (Claude oturum id'si Codex'e verilemez). Artık motor değişince imleç
-- SIFIRLANMAZ — motor başına ayrı zincir var; bota geri dönünce kaldığı
-- yerden sürer. Grup sohbetinin (ADIM 10) (bot, konuşma) imleç önkoşulunun
-- da aynı tablo deseniyle karşılanacağı yer.
CREATE TABLE bot_cursors (
    bot_id    uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    engine    text        NOT NULL DEFAULT 'claudecode',
    cursor    text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, engine)
);

-- Mevcut tek imleci botun O ANDAKİ motoruna taşı.
INSERT INTO bot_cursors (bot_id, engine, cursor)
SELECT id, engine, resume_cursor FROM bots WHERE resume_cursor <> '';

ALTER TABLE bots DROP COLUMN resume_cursor;

-- +goose Down
ALTER TABLE bots ADD COLUMN resume_cursor text NOT NULL DEFAULT '';
UPDATE bots b SET resume_cursor = c.cursor
FROM bot_cursors c
WHERE c.bot_id = b.id AND c.engine = b.engine;
DROP TABLE bot_cursors;