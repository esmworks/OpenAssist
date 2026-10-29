-- +goose Up
-- ADIM 6: botlar arası devir (PRD Ek B.2-6). reply_to_run_id: devri açan
-- koşuyu işaretler — alıcı botun cevabı çip olarak kaynağa döner. Depth-1
-- koruması runs.origin='bot' üzerinden işler (araç listesinden ask_bot düşer).
-- sender_bot_id: origin='bot' mesajlarında GÖNDEREN bot (alıcı sohbetinde
-- "kim sordu" bilgisi).
ALTER TABLE runs ADD COLUMN reply_to_run_id uuid REFERENCES runs(id) ON DELETE SET NULL;
ALTER TABLE messages ADD COLUMN sender_bot_id uuid REFERENCES bots(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE messages DROP COLUMN sender_bot_id;
ALTER TABLE runs DROP COLUMN reply_to_run_id;