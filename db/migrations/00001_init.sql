-- +goose Up
-- Faz 0 temeli: şema-bağımsız sistem bilgisi (kurulum damgası, bayraklar).
-- Alan tabloları (bots, runs, run_events, …) Faz 2'de gelir (PRD §8).
-- Tüm zaman sütunları timestamptz — UTC kuralı (PRD §6).
CREATE TABLE system_info (
    key        text        PRIMARY KEY,
    value      text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE system_info;
