-- +goose Up
-- Faz 7: bot katmanı — katmanlı hafıza, skills (nasıl) / routines (ne zaman)
-- ayrımı (K11), rutin motoru (K14) ve yerel işlem günlüğü (§8).

-- memories — katmanlı hafıza (scope: user | bot; ileride project). Agent
-- doğrudan yazamaz: öneri üretir (memory_save aracı), core doğrular
-- (dedup content_hash ile) ve kalıcılaştırır. Sistem promptuna scope
-- sırasıyla girer: önce user, sonra bot.
CREATE TABLE memories (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    scope         text        NOT NULL CHECK (scope IN ('user', 'bot')),
    bot_id        uuid        REFERENCES bots(id) ON DELETE CASCADE,
    kind          text        NOT NULL CHECK (kind IN ('fact', 'preference', 'instruction')),
    content       text        NOT NULL,
    -- content_hash — dedup anahtarı (core hesaplar, sha256 hex).
    content_hash  text        NOT NULL,
    source_run_id uuid        REFERENCES runs(id) ON DELETE SET NULL,
    confidence    real        NOT NULL DEFAULT 1.0,
    expires_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- bot scope'unda bot_id zorunlu; user scope'unda NULL.
    CHECK ((scope = 'bot') = (bot_id IS NOT NULL))
);
-- Aynı kapsamda aynı içerik bir kez (bot_id NULL için ayrı indeks —
-- Postgres UNIQUE NULL'ları eşit saymaz).
CREATE UNIQUE INDEX memories_dedup_bot_idx ON memories (scope, bot_id, content_hash)
    WHERE bot_id IS NOT NULL;
CREATE UNIQUE INDEX memories_dedup_user_idx ON memories (scope, content_hash)
    WHERE bot_id IS NULL;

-- skills — yeniden kullanılabilir talimat seti (K11: NASIL). Botlar arası
-- paylaşılır; bot bazında bot_skills ile etkinleştirilir.
CREATE TABLE skills (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text        NOT NULL UNIQUE,
    -- when_to_use — hangi durumda devreye girer (sistem promptuna girer).
    when_to_use  text        NOT NULL DEFAULT '',
    -- instructions — adımlar, doğrulama, çıktı formatı, onay gerektirenler.
    instructions text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bot_skills (
    bot_id     uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    skill_id   uuid        NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, skill_id)
);

-- routines — NE ZAMAN (K11/K14). Tetikleyici tipi baştan iki çeşit:
-- cron | interval+koşul. v1 yalnız cron'u işletir, şema ikisini de taşır
-- (K14: kolon eklemek kolay, model değiştirmek pahalı).
CREATE TABLE routines (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id           uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    name             text        NOT NULL,
    trigger_kind     text        NOT NULL DEFAULT 'cron' CHECK (trigger_kind IN ('cron', 'interval')),
    -- cron_expr + timezone — "hafta içi 18:00" UTC sunucuda doğru çalışsın
    -- diye tz DB'de saklanır (§6 saat dilimi tuzağı; örn. Europe/Istanbul).
    cron_expr        text        NOT NULL DEFAULT '',
    timezone         text        NOT NULL DEFAULT 'UTC',
    -- interval tetikleyici (v1'de işletilmez, şema taşır — K14).
    interval_seconds integer     NOT NULL DEFAULT 0,
    condition        text        NOT NULL DEFAULT '',
    -- Rutin tanımının zorunlu alanları (K11): girdi kaynağı, beklenen çıktı,
    -- onay sınırı, kaynak yoksa ne olacak.
    input_source     text        NOT NULL DEFAULT '',
    expected_output  text        NOT NULL DEFAULT '',
    approval_note    text        NOT NULL DEFAULT '',
    -- stale_policy: fail = kaynak yoksa hata bildir (varsayılan, K11);
    -- warn_stale = eski veriyle çalış ama AÇIKÇA söyle (K12 — sessiz bozulma yok).
    stale_policy     text        NOT NULL DEFAULT 'fail' CHECK (stale_policy IN ('fail', 'warn_stale')),
    enabled          boolean     NOT NULL DEFAULT true,
    next_run_at      timestamptz,
    last_run_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bot_id, name)
);
CREATE INDEX routines_due_idx ON routines (next_run_at) WHERE enabled;

-- routine_runs — çalışma geçmişi; rutin başına son ~20 kayıt tutulur
-- (PRD §9 limitleri), eskisi budanır.
CREATE TABLE routine_runs (
    id         bigserial   PRIMARY KEY,
    routine_id uuid        NOT NULL REFERENCES routines(id) ON DELETE CASCADE,
    run_id     uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX routine_runs_routine_idx ON routine_runs (routine_id, id DESC);

-- local_audit — Mac runner'da yapılan yerel işlemlerin günlüğü (§8
-- korelasyon zinciri: run_id → tool → sonuç). Koşu silinse de denetim
-- kaydı kalır (ON DELETE SET NULL).
CREATE TABLE local_audit (
    id         bigserial   PRIMARY KEY,
    run_id     uuid        REFERENCES runs(id) ON DELETE SET NULL,
    device     text        NOT NULL DEFAULT '',
    tool       text        NOT NULL,
    args       jsonb       NOT NULL DEFAULT '{}',
    ok         boolean     NOT NULL,
    result     text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX local_audit_time_idx ON local_audit (created_at DESC);

-- +goose Down
DROP TABLE local_audit;
DROP TABLE routine_runs;
DROP TABLE routines;
DROP TABLE bot_skills;
DROP TABLE skills;
DROP TABLE memories;
