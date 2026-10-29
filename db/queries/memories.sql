-- Bu dosyadaki yazma sorguları TEK BAŞINA çağrılmaz: hafıza satırı ile
-- revizyon satırı birlikte yazılmak zorundadır (K20). Giriş noktası
-- `store.SaveMemory` / `store.ReviseMemory` — ikisini bir transaction'da
-- sarar ve content_hash'i tek yerde hesaplar. Buradaki sorguları doğrudan
-- çağırmak geçmişi eksik bırakır.

-- name: UpsertMemory :one
-- Dedup content_hash ile: aynı içerik geldiyse güveni ve zamanı tazele
-- (agent önerir, core kalıcılaştırır — çakışmada yeni öneri kazanır).
-- Çakışmada `superseded_at` temizlenir: ajan bir olguyu bağımsız olarak
-- yeniden türettiyse o olgu yaşıyor demektir, birleştirilmiş halinden çıkar.
INSERT INTO memories (scope, bot_id, kind, content, content_hash, source_run_id, confidence, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (scope, bot_id, content_hash) WHERE bot_id IS NOT NULL
    DO UPDATE SET kind = EXCLUDED.kind, confidence = EXCLUDED.confidence,
                  expires_at = EXCLUDED.expires_at, updated_at = now(),
                  superseded_at = NULL, superseded_by = NULL
RETURNING *;

-- name: UpsertUserMemory :one
-- user scope'unda bot_id NULL — kısmi unique indeks ayrı ON CONFLICT ister.
INSERT INTO memories (scope, bot_id, kind, content, content_hash, source_run_id, confidence, expires_at)
VALUES ('user', NULL, $1, $2, $3, $4, $5, $6)
ON CONFLICT (scope, content_hash) WHERE bot_id IS NULL
    DO UPDATE SET kind = EXCLUDED.kind, confidence = EXCLUDED.confidence,
                  expires_at = EXCLUDED.expires_at, updated_at = now(),
                  superseded_at = NULL, superseded_by = NULL
RETURNING *;

-- name: ReviseMemoryContent :one
-- İçeriği DEĞİŞTİRİR ve revizyon sayacını artırır. Konsolidasyon turu ve geri
-- alma bu yoldan geçer; içerik ezen başka bir sorgu BİLİNÇLİ olarak yok (K20:
-- geri alınamayan birleştirme yapılmaz). content_hash içerikle birlikte
-- değişmek zorunda — dedup anahtarı o.
UPDATE memories
SET content = $2, content_hash = $3, revision = revision + 1, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RecordMemoryRevision :exec
-- Geçmiş satırı. Aynı revizyon iki kez yazılmaz (upsert çakışması içeriği
-- değiştirmediği için sayaç da artmaz) — `DO NOTHING` bunu sessizce geçer.
INSERT INTO memory_revisions (memory_id, revision, content, author, note, source_run_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (memory_id, revision) DO NOTHING;

-- name: SupersedeMemory :exec
-- Kaydı "başka kayda katıldı" diye işaretler. Silme değil: satır ve geçmişi
-- durur, yalnız prompta ve listelere girmez. Zaten katılmış kaydı tekrar
-- katmayız (superseded_at IS NULL koşulu) — zincir tek adımda kalır.
UPDATE memories
SET superseded_at = now(), superseded_by = $2, updated_at = now()
WHERE id = $1 AND superseded_at IS NULL;

-- name: RestoreMemory :exec
-- Katılmayı geri alır (kötü konsolidasyon turundan dönüş).
UPDATE memories
SET superseded_at = NULL, superseded_by = NULL, updated_at = now()
WHERE id = $1;

-- name: ListMemoriesForPrompt :many
-- Sistem promptu sırası: önce user, sonra bot (PRD §8). Süresi dolan ve başka
-- kayda katılan girmez.
--
-- İki katmanlı sıralama bilinçli:
--   İç sorgu SEÇER — scope önceliği (user kayıtları önce yer kapar) + kayıt
--   içinde `created_at DESC`. DESC şart: eskiden ASC'ti, yani limit dolduğunda
--   EN ESKİ kayıtlar alınıyordu ve hafıza sınırı geçtiği gün sonradan yazılan
--   hiçbir kayıt prompta girmiyordu — sessizce, hata vermeden.
--   Dış sorgu YAZAR — seçilenleri kronolojik sıraya çevirir; prompt eskiden
--   yeniye okunur, yeni kayıt en altta (motor için doğal okuma yönü).
SELECT * FROM (
    SELECT * FROM memories
    WHERE (scope = 'user' OR (scope = 'bot' AND bot_id = $1))
      AND (expires_at IS NULL OR expires_at > now())
      AND superseded_at IS NULL
    ORDER BY (scope = 'user') DESC, created_at DESC
    LIMIT $2
) secilen
ORDER BY (scope = 'user') DESC, created_at;

-- name: ListMemoriesForPromptSince :many
-- Özet belgesi üretildikten SONRA yazılan ya da değişen kayıtlar.
--
-- Neden gerekli: özet gece üretilir, ham satırlar özet varken basılmaz. Bu
-- sorgu olmadan gün içinde kaydedilen bir tercih ("bana Deniz de") bir
-- sonraki konsolidasyon turuna kadar (≥20 saat) sistem promptuna hiç girmezdi
-- — kullanıcı bota söylüyor, bot ertesi güne kadar bilmiyor.
--
-- updated_at kullanılır (created_at değil): geri alınan ya da tazelenen bir
-- kayıt da özetin anlattığından farklıdır, prompta taze haliyle girmeli.
SELECT * FROM (
    SELECT * FROM memories
    WHERE (scope = 'user' OR (scope = 'bot' AND bot_id = $1))
      AND (expires_at IS NULL OR expires_at > now())
      AND superseded_at IS NULL
      AND updated_at > sqlc.arg('since')
    ORDER BY (scope = 'user') DESC, updated_at DESC
    LIMIT $2
) secilen
ORDER BY (scope = 'user') DESC, created_at;

-- name: ListMemories :many
SELECT * FROM memories WHERE superseded_at IS NULL ORDER BY created_at DESC LIMIT $1;

-- name: SearchMemories :many
-- Basit içerik araması (K20: gömme/vektör yok — v1 hacmi indeks istemiyor).
-- bot_id verilirse o botun kayıtları + user scope; verilmezse tüm kapsamlar.
--
-- Arama metni ILIKE joker karakterlerinden KAÇIRILIR: kaçırılmasaydı "50%"
-- araması tüm kayıtları döndürür, ajan da bunu "bu konuda çok şey biliyorum"
-- diye okuyup alakasız 50 kaydı bağlama alırdı.
--
-- arsiv=true emekliye ayrılmış (superseded) kayıtları arar: kötü bir
-- konsolidasyon turundan sonra katılan kaydı bulup geri getirmenin tek yolu
-- (memory_restore) — id'sini bulamayan kullanıcı geri de alamaz.
SELECT * FROM memories
WHERE (CASE WHEN sqlc.arg('arsiv')::bool THEN superseded_at IS NOT NULL ELSE superseded_at IS NULL END)
  AND (expires_at IS NULL OR expires_at > now())
  AND (sqlc.narg('bot_id')::uuid IS NULL OR scope = 'user' OR bot_id = sqlc.narg('bot_id')::uuid)
  AND content ILIKE '%' || replace(replace(replace(sqlc.arg('q')::text, '\', '\\'), '%', '\%'), '_', '\_') || '%' ESCAPE '\'
ORDER BY (scope = 'user') DESC, created_at DESC
LIMIT sqlc.arg('lim');

-- name: ListMemoriesForConsolidation :many
-- Konsolidasyon turunun girdisi: bir botun görebildiği yaşayan kayıtlar,
-- limitsiz (tur hepsini görmeli — prompt bütçesi turun kendi işi).
--
-- SABİT TALİMATLAR (kind='instruction') BU LİSTEDE YOK. Konsolidasyon bir
-- damıtma turudur: benzer kayıtları birleştirir, bayat olanı eler. Bir kural
-- ("asla şu dizine yazma") damıtılacak bir olgu değil, uyulacak bir emirdir;
-- "yakın" bir başka kuralla birleşirse anlamı bozulur, uzun süre tetiklenmedi
-- diye bayat sayılırsa sessizce kaybolur. Listede olmadıkları için plan
-- doğrulaması da onlara dokunan bir planı reddeder (id bizim verdiğimiz
-- listede olmalı) — kilit iki katmanlı.
SELECT * FROM memories
WHERE (scope = 'user' OR (scope = 'bot' AND bot_id = $1))
  AND kind <> 'instruction'
  AND (expires_at IS NULL OR expires_at > now())
  AND superseded_at IS NULL
ORDER BY (scope = 'user') DESC, created_at;

-- name: ListStandingInstructions :many
-- Sabit talimatlar — sistem promptuna HER koşuda giren, kullanıcının kendi
-- yazdığı kurallar.
--
-- Neden özetten ayrı bir yol: geri çağırma olasılıksaldır. Özet damıtılırken
-- bir kural eriyebilir; `memory_search` ise ancak model aramayı akıl ederse
-- çalışır — ve bir YASAK için tam da o an modelin arama yapmak için sebebi
-- yoktur. Tercih kaçırılan aramayı affeder, yasak affetmez.
--
-- Bu blok yalnız kullanıcının doldurabildiği bir yer: konsolidasyon kayıtları
-- görmez, `memory_save` 'instruction' kabul etmez. Aksi halde okunan bir
-- sayfadan gelen "bundan sonra hep şunu yap" cümlesi kalıcı VE korumalı bir
-- talimata dönüşürdü — hafızanın en güvenilir köşesi en kolay zehirlenen yer
-- olurdu.
SELECT * FROM memories
WHERE kind = 'instruction'
  AND (scope = 'user' OR (scope = 'bot' AND bot_id = $1))
  AND (expires_at IS NULL OR expires_at > now())
  AND superseded_at IS NULL
ORDER BY (scope = 'user') DESC, created_at
LIMIT $2;

-- name: ListMemoryRevisions :many
SELECT * FROM memory_revisions WHERE memory_id = $1 ORDER BY revision DESC;

-- name: GetMemoryRevision :one
SELECT * FROM memory_revisions WHERE memory_id = $1 AND revision = $2;

-- name: GetMemory :one
SELECT * FROM memories WHERE id = $1;

-- name: DeleteMemory :execrows
DELETE FROM memories WHERE id = $1;

-- name: ReapExpiredMemories :execrows
DELETE FROM memories WHERE expires_at IS NOT NULL AND expires_at <= now();

-- name: UpsertMemorySummary :one
-- Bot başına tek özet belge; sistem promptuna bunun gövdesi girer (K20).
INSERT INTO memory_summaries (bot_id, content, source_count)
VALUES ($1, $2, $3)
ON CONFLICT (bot_id) DO UPDATE
    SET content = EXCLUDED.content, source_count = EXCLUDED.source_count,
        generated_at = now(), updated_at = now()
RETURNING *;

-- name: GetMemorySummary :one
SELECT * FROM memory_summaries WHERE bot_id = $1;

-- name: DeleteMemorySummary :exec
DELETE FROM memory_summaries WHERE bot_id = $1;
