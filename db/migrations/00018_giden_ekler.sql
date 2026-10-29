-- +goose Up
-- ADIM 13: ekler artık ÇİFT YÖNLÜ. Bugüne kadar ek yalnız kullanıcıdan bota
-- akıyordu (upload → mesaj → motor promptunda yol); botun ürettiği bir dosya
-- (rapor, grafik, dönüştürülmüş belge) sohbete hiç düşemiyordu — kullanıcı
-- "hani dosya?" diye sorduğunda cevap "sunucuda bir yerde" oluyordu.
--
-- run_id neden gerekli: giden ek KOŞU SIRASINDA doğar, oysa botun mesajı koşu
-- BİTİNCE yazılır. Aradaki ek satırının bir sahibi olmalı; ek id'lerini süreç
-- belleğinde tutmak park/resume'da (ve sunucu yeniden başladığında) kaybolurdu.
-- Koşuya bağlanan satır ise mesaj yazılırken tek UPDATE ile bağlanır ve
-- Telegram giden dosyayı aynı anahtardan bulur.
--
-- ON DELETE SET NULL: koşu silinse de dosya mesajda asılı kalır (mesaj varsa
-- ek de vardır); tersi olsaydı geçmiş bir sohbetteki ek koşu temizliğinde
-- sessizce yok olurdu.
ALTER TABLE attachments ADD COLUMN run_id uuid REFERENCES runs(id) ON DELETE SET NULL;

-- Kısmi indeks: sorgulanan tek şey "bu koşunun HENÜZ BAĞLANMAMIŞ ekleri".
CREATE INDEX attachments_run_idx ON attachments (run_id) WHERE message_id IS NULL;

-- +goose Down
DROP INDEX attachments_run_idx;
ALTER TABLE attachments DROP COLUMN run_id;
