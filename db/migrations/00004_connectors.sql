-- +goose Up
-- Faz 4: konektörler — Google OAuth token'ları durağan halde AES-256-GCM ile
-- mühürlü durur (§5, MASTER_KEY). Tek kullanıcılı sistem: konektör globaldir,
-- (kind, account) benzersizdir.
CREATE TABLE connectors (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind                 text NOT NULL,                -- 'google'
    account              text NOT NULL DEFAULT '',     -- bağlı hesabın e-postası
    status               text NOT NULL DEFAULT 'connected'
                              CHECK (status IN ('connected', 'error')),
    -- AES-256-GCM mühürlü refresh token (base64: nonce||ciphertext).
    sealed_refresh_token text NOT NULL,
    scopes               text[] NOT NULL DEFAULT '{}',
    error                text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, account)
);

-- +goose Down
DROP TABLE connectors;
