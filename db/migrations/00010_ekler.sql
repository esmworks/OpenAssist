-- +goose Up
-- ADIM 5: ekler (PRD §8 — dosya metadata + disk yolu). Motor multimodal değil
-- (ADIM 9); v1 semantiği: ek = sunucuda dosya + motora yol/not olarak iletilir
-- (claude CLI sunucuda koşar, dosyaya doğrudan erişir — PDF'i read_file ile
-- okuyabilir).
CREATE TABLE attachments (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id     uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    -- message_id: upload→send akışında önce NULL, mesaj yazılınca bağlanır.
    message_id uuid        REFERENCES messages(id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('pdf','image','doc','file')),
    name       text        NOT NULL,
    size       bigint      NOT NULL,
    -- path: DataDir/attachments altına GÖRELİ yol. Dosya adı id tabanlıdır,
    -- istemci adı asla yola girmez (path traversal imkânsız); gerçek ad
    -- name'de yaşar.
    path       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_message_idx ON attachments (message_id);

-- +goose Down
DROP TABLE attachments;