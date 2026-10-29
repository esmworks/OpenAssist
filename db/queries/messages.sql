-- name: CreateMessage :one
-- origin: mesajın geldiği kanal (api | telegram | mac | routine | bot, K16).
-- bot mesajları ve devir çipleri de buradan yazılır (role='bot'/'event').
-- sender_bot_id: origin='bot' mesajlarında GÖNDEREN bot (ADIM 6).
INSERT INTO messages (bot_id, conversation_id, run_id, role, text, origin, sender_bot_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListMessages :many
-- Görünür akış: superseded (düzenlenmiş eski sürüm) satırları düşer (ADIM 9).
SELECT * FROM messages
WHERE conversation_id = $1 AND superseded_by IS NULL
ORDER BY created_at DESC LIMIT $2;

-- name: GetMessage :one
SELECT * FROM messages WHERE id = $1;

-- name: SearchMessages :many
-- Sohbet geçmişi araması: "geçen ay bunu konuşmuştuk" sorusunun cevabı
-- hafızada değil konuşmanın kendisindedir. Hafıza araması damıtılmış kalıcı
-- olguları görür; bu sorgu ham mesajları.
--
-- ILIKE değil FTS: mesaj hacmi hafıza hacmiyle aynı değil (hafızada bot başına
-- onlarca kayıt var, mesajlarda on binlerce satır olabilir) — her aramada tam
-- tablo taraması sürdürülemez. İndeks 00017'de.
--
-- Sorgu ÖNEK eşleşmesiyle kurulur ('kelime':*), stemmer ile değil. Türkçe
-- sondan eklemeli: kullanıcı "indeks" yazar, mesajda "indeksi"/"indeksini"
-- geçer. Snowball'ın turkish stemmer'ı bu işi yapamıyor — ölçüldü: "hafızayı"
-- belgede 'hafıza'ya inerken sorgudaki "hafıza" 'hafız'a iniyor ve kullanıcı
-- kendi cümlesini bulamıyor.
--
-- Sorgu kullanıcı metninden ÜRETİLİR, doğrudan to_tsquery'ye verilmez:
-- girdi önce to_tsvector('simple', …) ile lexeme'lere ayrılır (noktalama ve
-- işleçler orada temizlenir), sonra her lexeme quote_literal ile tırnaklanıp
-- ':*' alır. Ham to_tsquery kullanıcının yazdığı ")" ya da "&" karakterinde
-- patlar ve arama kutusu 500 döndürürdü; bu yol bozuk girdiyi sessizce
-- normalize eder. Girdiden tek bir lexeme çıkmazsa sorgu NULL olur ve hiçbir
-- satır eşleşmez (boş sonuç, hata değil).
--
-- İki filtre gizlilik/gürültü kararı: superseded_by IS NULL — düzenlenmiş
-- mesajın eski sürümü akışta görünmüyor, aramada da görünmemeli (kullanıcı
-- sildiği cümleyi karşısında bulmasın); role='event' düşer — sistem satırları
-- ("koşu iptal edildi") sonucu kirletir.
WITH sorgu AS (
    SELECT to_tsquery('simple', string_agg(quote_literal(l) || ':*', ' & ')) AS q
    FROM unnest(tsvector_to_array(to_tsvector('simple', sqlc.arg('q')::text))) AS l
)
SELECT m.*, ts_rank(to_tsvector('simple', m.text), sorgu.q) AS siralama
FROM messages m, sorgu
WHERE sorgu.q IS NOT NULL
  AND to_tsvector('simple', m.text) @@ sorgu.q
  AND m.superseded_by IS NULL
  AND m.role <> 'event'
  AND (sqlc.narg('bot_id')::uuid IS NULL OR m.bot_id = sqlc.narg('bot_id')::uuid)
ORDER BY siralama DESC, m.created_at DESC
LIMIT sqlc.arg('lim');

-- name: SupersedeMessage :execrows
-- Yalnız ilk düzenleme kazanır: çifte düzenleme yarışında ikinci satır 0 döner
-- ve çağıran transaction'ı geri sarar (ya hep ya hiç, ADIM 9).
UPDATE messages SET superseded_by = $2 WHERE id = $1 AND superseded_by IS NULL;

-- name: CreateEditedMessage :one
-- Düzenlenmiş sürüm: parent zinciri eski mesaja kurulur; idempotency anahtarı
-- DÜZENLEMEYE aittir (aynı düzenlemenin retry'i yeni dal açmaz).
INSERT INTO messages (bot_id, conversation_id, run_id, role, text, origin, idempotency_key, parent_id)
VALUES ($1, $2, $3, 'user', $4, $5, $6, $7)
RETURNING *;

-- name: CreateChatMessage :one
-- Kullanıcı mesajı, idempotency kapısıyla (ADIM 3). Aynı (bot, origin, key)
-- üçlüsü zaten kayıtlıysa satır YAZILMAZ ve sıfır satır döner — çağıran
-- transaction'ı geri sarıp ilk gönderimin koşusunu döndürmelidir.
INSERT INTO messages (bot_id, conversation_id, run_id, role, text, origin, idempotency_key)
VALUES ($1, $2, $3, 'user', $4, $5, $6)
ON CONFLICT (bot_id, origin, idempotency_key) WHERE idempotency_key <> '' DO NOTHING
RETURNING *;

-- name: GetChatMessageByKey :one
SELECT * FROM messages
WHERE bot_id = $1 AND origin = $2 AND idempotency_key = $3 AND idempotency_key <> ''
LIMIT 1;
-- name: ListMessagesUntil :many
-- Düzenleme noktasına kadarki görünür sohbet (ADIM 9'da satır içi tekrar
-- bağlamı için; DESC döner, çağıran kronolojik sıraya çevirir).
SELECT * FROM messages
WHERE conversation_id = $1 AND created_at <= $2 AND id <> $3 AND superseded_by IS NULL
ORDER BY created_at DESC LIMIT $4;

-- name: DeleteConversationMessages :exec
-- Sohbeti temizle (K19 yüzeyi): konuşmanın tüm mesajları gider — düzenleme
-- zinciri (parent/superseded) dahil. Ek satırları ON DELETE CASCADE ile düşer;
-- disk dosyalarını çağıran, önden topladığı yollardan siler.
DELETE FROM messages WHERE conversation_id = $1;
