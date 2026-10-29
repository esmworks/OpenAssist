-- +goose Up
-- Faz 2 çekirdeği: bot/ekip, mesaj, koşu ve koşu olayları (PRD §8; alan
-- seçimleri tasarım paketiyle hizalı — docs/design/2026-08-13-faz2-cekirdek.md).

CREATE TABLE bots (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            text        NOT NULL DEFAULT 'bot' CHECK (kind IN ('bot','team')),
    name            text        NOT NULL,
    -- description → --append-system-prompt (K15): botun görevi/kalıcı kuralları
    description     text        NOT NULL DEFAULT '',
    -- system_prompt: "Gelişmiş — talimat metni" (tasarım §6)
    system_prompt   text        NOT NULL DEFAULT '',
    color_primary   text        NOT NULL DEFAULT '#b18cf0',
    color_secondary text        NOT NULL DEFAULT '#7b4fd6',
    section         text        NOT NULL DEFAULT '',
    engine          text        NOT NULL DEFAULT 'claudecode',
    model           text        NOT NULL DEFAULT '',
    temperature     double precision, -- tasarım: Yaratıcılık 0–1 (motor destekliyorsa)
    allowed_tools   text[]      NOT NULL DEFAULT '{}',
    -- bot ≈ adlandırılmış kalıcı oturum: son koşunun imleci burada yaşar
    resume_cursor   text        NOT NULL DEFAULT '',
    pinned          boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id   uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    member_id uuid NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    position  int  NOT NULL DEFAULT 0,
    PRIMARY KEY (team_id, member_id)
);

CREATE TABLE runs (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id         uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    status         text        NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','running','approval_pending','completed','failed','canceled')),
    prompt         text        NOT NULL,
    engine         text        NOT NULL,
    model          text        NOT NULL DEFAULT '',
    -- resume_cursor: koşu BAŞLARKEN kullanılacak imleç; result_cursor: koşu
    -- sonunda motorun döndürdüğü imleç (park→resume zinciri bunlarla kurulur)
    resume_cursor  text        NOT NULL DEFAULT '',
    result_cursor  text        NOT NULL DEFAULT '',
    -- park edilen istek (tool + input) — Onay Merkezi kartının kaynağı
    pending_ask    jsonb,
    -- onay kararı: resume koşusuna taşınan tek kullanımlık grant (K15)
    grant_decision text        NOT NULL DEFAULT '' CHECK (grant_decision IN ('','allow','deny')),
    error          text        NOT NULL DEFAULT '',
    cost_usd       double precision NOT NULL DEFAULT 0,
    input_tokens   bigint      NOT NULL DEFAULT 0,
    output_tokens  bigint      NOT NULL DEFAULT 0,
    num_turns      int         NOT NULL DEFAULT 0,
    final_text     text        NOT NULL DEFAULT '',
    -- kuyruk hijyeni (PRD §6): sahiplik + kalp atışı + tekrar deneme sayacı
    attempt        int         NOT NULL DEFAULT 0,
    claimed_by     text        NOT NULL DEFAULT '',
    heartbeat_at   timestamptz,
    queued_at      timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    finished_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX runs_queue_idx ON runs (status, queued_at);
CREATE INDEX runs_bot_idx   ON runs (bot_id, created_at DESC);

CREATE TABLE messages (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id     uuid        NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    run_id     uuid        REFERENCES runs(id) ON DELETE SET NULL,
    role       text        NOT NULL CHECK (role IN ('user','bot','event')),
    text       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_bot_idx ON messages (bot_id, created_at);

CREATE TABLE run_events (
    -- bigserial: SSE Last-Event-ID olarak da kullanılır (kaldığın yerden devam)
    id         bigserial   PRIMARY KEY,
    run_id     uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    payload    jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX run_events_run_idx ON run_events (run_id, id);

-- +goose Down
DROP TABLE run_events;
DROP TABLE messages;
DROP TABLE runs;
DROP TABLE team_members;
DROP TABLE bots;
