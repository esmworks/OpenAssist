-- +goose Up
-- Hafızaya iki ekleme: düzeltme kaydı ve sohbet geçmişi araması.
--
-- 1) kind='correction' — kullanıcının botu düzelttiği an ("hayır, pnpm kullan")
--    hafızanın en değerli girdisidir: tekrar edilmemesi gereken bir hatayı
--    işaret eder ve kullanıcı onu bir kez söylediğini varsayar. 'fact' olarak
--    yazılınca diğer olgularla aynı ağırlığa düşüyordu; ayrı bir kind
--    konsolidasyona ve prompta "bu bir ders" diye görünmesini sağlar.
--
-- 2) messages üzerinde tam metin indeksi — "geçen ay şunu konuşmuştuk"
--    sorusunun cevabı bugün hiçbir yerde aranamıyor. Hafıza araması yalnız
--    damıtılmış kalıcı olguları görür; konuşmanın kendisi Postgres'te durur
--    ama ILIKE ile taranamaz (mesaj hacmi hafıza hacmiyle aynı değil).
--
--    Sözlük 'simple', 'turkish' DEĞİL — ölçülerek seçildi. Snowball'ın Türkçe
--    stemmer'ı tutarsız: "hafızayı" belgede 'hafıza' lexeme'ine iner ama
--    sorgudaki "hafıza" 'hafız'a iner, yani kullanıcının kendi yazdığı kelime
--    kendi mesajını BULAMAZ. "indeksini" → 'indeks' soyuluyorken "indeksi"
--    olduğu gibi kalıyor.
--
--    Türkçe sondan eklemeli bir dil, o yüzden doğru araç stemmer değil ÖNEK
--    eşleşmesi: sorgu terimleri ':*' ile aranır (bkz. SearchMessages), böylece
--    "indeks" hem "indeksi" hem "indeksini" geçen mesajı bulur ve belge ile
--    sorgu aynı sözlükten geçtiği için sonuç öngörülebilir olur.
ALTER TABLE memories DROP CONSTRAINT memories_kind_check;
ALTER TABLE memories ADD CONSTRAINT memories_kind_check
    CHECK (kind IN ('fact', 'preference', 'instruction', 'correction'));

CREATE INDEX messages_fts_idx ON messages USING GIN (to_tsvector('simple', text));

-- +goose Down
DROP INDEX messages_fts_idx;
ALTER TABLE memories DROP CONSTRAINT memories_kind_check;
-- Geri dönüşte 'correction' kayıtları CHECK'i düşürmesin: olgu sayılırlar.
UPDATE memories SET kind = 'fact' WHERE kind = 'correction';
ALTER TABLE memories ADD CONSTRAINT memories_kind_check
    CHECK (kind IN ('fact', 'preference', 'instruction'));
