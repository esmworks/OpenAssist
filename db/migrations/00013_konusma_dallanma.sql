-- +goose Up
-- ADIM 9 — konuşma dallanma, ilk aşama: düzenle + yeniden üret.
--
-- parent_id: düzenlenen sürümün kaynak mesaja bağı (ağaç ileride tam
-- dallanmaya açılır — ADIM 9 ikinci aşama serbest dallanma).
-- superseded_by: eski sürüm gösterimden düşer ama satır DURUR (hafıza
-- revizyonlarıyla aynı ilke: geçmiş asla silinmez, yalnız görünmez olur).
ALTER TABLE messages ADD COLUMN parent_id uuid REFERENCES messages(id) ON DELETE SET NULL;
ALTER TABLE messages ADD COLUMN superseded_by uuid REFERENCES messages(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE messages DROP COLUMN superseded_by;
ALTER TABLE messages DROP COLUMN parent_id;