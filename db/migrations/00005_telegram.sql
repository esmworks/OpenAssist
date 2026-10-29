-- +goose Up
-- Faz 5: Telegram gateway (K16). Telegram bir KANALDIR: mesajlar aynı
-- runs/messages yolundan akar, kanal yalnız messages.origin ile ayrışır.
ALTER TABLE messages ADD COLUMN origin text NOT NULL DEFAULT 'api';

-- runs.origin — koşuyu doğuran kanal. Park edilen koşu saatler sonra resume
-- olduğunda sonucun hangi kanala bildirileceği buradan bilinir (süreç yeniden
-- başlamış olabilir; bellekteki eşleme yetmez).
ALTER TABLE runs ADD COLUMN origin text NOT NULL DEFAULT 'api';

-- telegram_chats — allowlist + sohbetin aktif botu (/talk ile değişir).
-- Satır yoksa sohbet izinli DEĞİLDİR (fail-closed, K16 güvenlik).
CREATE TABLE telegram_chats (
    chat_id       bigint      PRIMARY KEY,
    label         text        NOT NULL DEFAULT '',
    active_bot_id uuid        REFERENCES bots(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- approval_tokens — onay butonlarının callback_data'sı tahmin edilemez ve
-- TEK KULLANIMLIKtır (K16): "approve_183" gibi öngörülebilir id kullanılmaz.
CREATE TABLE approval_tokens (
    token      text        PRIMARY KEY,
    run_id     uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    action     text        NOT NULL CHECK (action IN ('allow', 'deny', 'allow_always', 'answer')),
    -- answer: 'answer' eyleminde şıkkın metni (soru kartı butonları).
    answer     text        NOT NULL DEFAULT '',
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_tokens_run_idx ON approval_tokens (run_id);

-- telegram_cards — hangi koşunun onay kartı hangi mesajda; başka kanaldan
-- karar gelince kart güncellenir (çok kanallı yarış, K16).
CREATE TABLE telegram_cards (
    run_id     uuid        PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    chat_id    bigint      NOT NULL,
    message_id bigint      NOT NULL,
    resolved   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE telegram_cards;
DROP TABLE approval_tokens;
DROP TABLE telegram_chats;
ALTER TABLE messages DROP COLUMN origin;
