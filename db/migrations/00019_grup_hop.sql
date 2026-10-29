-- +goose Up
-- ADIM 10 ikinci aşama — grup-içi @mention zinciri: bir ekip üyesinin cevabı
-- başka bir üyeyi @Adı ile çağırabilir ve o üye AYNI grup kanalında koşar.
-- Zincirin derinliği koşuda taşınır: kullanıcı mesajı hop=0, onun tetiklediği
-- üye hop=1, o da birini çağırırsa hop=2… `MAX_GROUP_HOPS`'a varınca durur.
--
-- Neden koşuda bir sayaç: sınır olmadan iki bot birbirini "sağ ol / rica
-- ederim" diye sonsuza kadar çağırabilir — her tur gerçek para. Sayaç
-- reply_to_run_id zincirinden de türetilebilirdi ama her tetiklemede zincir
-- geriye yürümek gerekirdi; park/resume koşuları da zinciri kırıyor.
ALTER TABLE runs ADD COLUMN group_hop int NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE runs DROP COLUMN group_hop;
