-- +goose Up
-- Faz 11 / K20: hafızanın revizyon geçmişi ve özet belgesi.
--
-- Neden şimdi: bugüne kadar `content_hash` dedup ANAHTARI olduğu için hafıza
-- içeriği hiç değişmiyordu — çakışma yalnız güveni/süreyi tazeliyordu. Gece
-- konsolidasyon turu bunu bitiriyor: tur satırları birleştirecek, çakışanı
-- eleyecek, özet üretecek. Geçmiş tutulmazsa kötü bir tur hafızayı sessizce
-- yok eder ve kanıtı kalmaz. Bu göç konsolidasyondan ÖNCE gelir.

ALTER TABLE memories
    -- revision — kaçıncı içerik sürümü. Upsert (aynı içerik tekrar önerildi)
    -- artırmaz; yalnız içerik DEĞİŞTİĞİNDE artar.
    ADD COLUMN revision      int NOT NULL DEFAULT 1,
    -- superseded_at — bu kayıt başka bir kayda katıldı. Silme DEĞİL: satır
    -- durur, geçmişi durur, yalnız prompta ve listelere girmez.
    ADD COLUMN superseded_at timestamptz,
    -- superseded_by — hangi kayda katıldığı. Yalnızca işaretçi: yaşayıp
    -- yaşamama kararı superseded_at'tedir. Hedef kayıt sonradan silinirse bu
    -- alan NULL'a düşer ama kayıt DİRİLMEZ — aksi halde birleştirilmiş bir
    -- olgu, birleştiği kaydın silinmesiyle sessizce geri gelirdi.
    ADD COLUMN superseded_by uuid REFERENCES memories(id) ON DELETE SET NULL;

-- Yaşayan kayıtların sorgusu kısmi indeksten geçsin (prompt her koşuda okur).
CREATE INDEX memories_yasayan_idx ON memories (scope, bot_id, created_at DESC)
    WHERE superseded_at IS NULL;

-- memory_revisions — içerik geçmişi, append-only. Geri alma da yeni bir
-- revizyon yazar (sayaç geri sarılmaz): "ne zaman neye döndük" izlenebilir
-- kalır. Kayıt silinirse geçmişi de gider (CASCADE) — kullanıcı bir olguyu
-- sildiyse metni arşivde tutmak sürpriz olurdu.
CREATE TABLE memory_revisions (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id     uuid        NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    revision      int         NOT NULL,
    content       text        NOT NULL,
    -- author — kim yazdı: ajan önerisi, gece konsolidasyonu, ya da kullanıcı
    -- (geri alma / elle düzeltme). Kötü bir konsolidasyon turunu bu alandan
    -- ayıklarız.
    author        text        NOT NULL CHECK (author IN ('agent', 'consolidation', 'user')),
    note          text        NOT NULL DEFAULT '',
    source_run_id uuid        REFERENCES runs(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (memory_id, revision)
);
CREATE INDEX memory_revisions_zaman_idx ON memory_revisions (memory_id, revision DESC);

-- Var olan kayıtlar için 1. revizyon: zincir baştan tam olsun, geçmiş bu
-- göçten önce başlamış gibi görünmesin.
INSERT INTO memory_revisions (memory_id, revision, content, author, source_run_id, created_at)
SELECT id, 1, content, 'agent', source_run_id, created_at FROM memories;

-- memory_summaries — bot başına TEK özet belge; sistem promptuna artık bunun
-- gövdesi girer, 50 satırlık düz döküm değil (K20). Yalnız konsolidasyon turu
-- yazar → prompt öneki koşular arası kararlı kalır ve motorun KV önbelleği
-- her koşuda baştan ısınmaz.
CREATE TABLE memory_summaries (
    bot_id      uuid        PRIMARY KEY REFERENCES bots(id) ON DELETE CASCADE,
    content     text        NOT NULL,
    -- source_count — özetin kaç hafıza satırından damıtıldığı; tur sonrası
    -- "özet bayatladı mı" kararı buradan verilir.
    source_count int        NOT NULL DEFAULT 0,
    generated_at timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE memory_summaries;
DROP TABLE memory_revisions;
DROP INDEX memories_yasayan_idx;
ALTER TABLE memories
    DROP COLUMN superseded_by,
    DROP COLUMN superseded_at,
    DROP COLUMN revision;
