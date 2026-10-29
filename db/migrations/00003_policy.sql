-- +goose Up
-- Faz 3: onay sistemi + güvenlik çekirdeği (PRD K17, §5.1, Ek B.3).

-- Tainted bayrağı (§5.1): koşu dış içerik okuduysa işaretlenir; yan-etkili
-- araçlarda "hep izin ver" kuralları geçersizleşir.
ALTER TABLE runs ADD COLUMN tainted boolean NOT NULL DEFAULT false;

-- 'expired' durumu: onay TTL'i dolan park edilmiş koşular (K14 ritmi:
-- süresiz bekleyen onay kartı birikintiye dönüşmesin).
ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('queued','running','approval_pending','completed','failed','canceled','expired'));

-- Onay kuralları (K17): "hep izin ver / hep reddet". bot_id NULL = tüm botlar.
-- Yüksek riskli sınıflara (execute/destructive) kural YAZILMAZ — uygulama
-- katmanı reddeder; tainted koşuda allow kuralları okunmaz.
CREATE TABLE approval_rules (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id         uuid        REFERENCES bots(id) ON DELETE CASCADE,
    tool           text        NOT NULL,
    action         text        NOT NULL CHECK (action IN ('allow','deny')),
    note           text        NOT NULL DEFAULT '',
    created_by_run uuid        REFERENCES runs(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (bot_id, tool)
);

-- Eylem günlüğü: izin verilen her yan-etkili araç çağrısı (kota sayacı +
-- Aktivite yüzeyinin ham verisi).
CREATE TABLE action_log (
    id         bigserial   PRIMARY KEY,
    bot_id     uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    run_id     uuid        REFERENCES runs(id) ON DELETE SET NULL,
    tool       text        NOT NULL,
    class      text        NOT NULL,
    decided_by text        NOT NULL, -- rule | api | grant
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX action_log_bot_day_idx ON action_log (bot_id, created_at);

-- Webhook çift teslimat kalkanı (§6): (kaynak, olay id) bir kez işlenir.
CREATE TABLE webhook_events (
    source      text        NOT NULL,
    event_id    text        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, event_id)
);

-- +goose Down
DROP TABLE webhook_events;
DROP TABLE action_log;
DROP TABLE approval_rules;
ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('queued','running','approval_pending','completed','failed','canceled'));
ALTER TABLE runs DROP COLUMN tainted;
