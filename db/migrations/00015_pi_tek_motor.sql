-- +goose Up
-- Tek motor pi: claudecode/opencode/codex adaptörleri kaldırıldı.
--
-- Mevcut satırlar taşınır çünkü motor adı artık kayıtlı DEĞİL: orkestratör
-- bilinmeyen motorda koşuyu "motor yok" ile düşürür ve kullanıcı botunu
-- düzenlemeden hiçbir şey koşturamaz.
--
-- İmleçler ise TAŞINMAZ, SİLİNİR: imleç opak bir motor detayıdır
-- (claudecode session_id, codex thread id, pi oturum kimliği) ve bir motorun
-- imlecini diğerine vermek "oturum bulunamadı" hatası üretir. Silinen imleç
-- yalnız "bir sonraki tur yeni oturum açar" demektir; mesaj geçmişi
-- messages tablosundadır, kaybolmaz.

ALTER TABLE bots ALTER COLUMN engine SET DEFAULT 'pi';
UPDATE bots SET engine = 'pi' WHERE engine <> 'pi';

DELETE FROM bot_cursors WHERE engine <> 'pi';
ALTER TABLE bot_cursors ALTER COLUMN engine SET DEFAULT 'pi';

-- Model adları da motora özgüydü (ör. "anthropic/claude-sonnet-4"); pi'nin
-- kataloğunda karşılıkları yok. Boşaltılınca bot varsayılan modele düşer,
-- kullanıcı Motorlar ekranından yenisini seçer.
UPDATE bots SET model = '' WHERE model <> '';

-- Bot başına pi eklenti allowlist'i. Keşif kapalı olduğu için bu liste
-- botun neyi yükleyeceğinin TEK kaynağıdır; boş liste = yalnız çekirdek
-- araçlar. Varsayılan oa-gateway: onay kapısı ve OpenAssist araçları
-- olmadan bot ne izin sorar ne de gateway'i görür.
ALTER TABLE bots ADD COLUMN extensions text[] NOT NULL DEFAULT '{oa-gateway.ts}';
UPDATE bots SET extensions = '{oa-gateway.ts}';

-- +goose Down
ALTER TABLE bots DROP COLUMN extensions;
ALTER TABLE bot_cursors ALTER COLUMN engine SET DEFAULT 'claudecode';
ALTER TABLE bots ALTER COLUMN engine SET DEFAULT 'claudecode';
