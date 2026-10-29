-- name: CreateBot :one
-- extensions'ta coalesce BİLİNÇLİ: Go'da nil dilim SQL'de NULL'dur ve sütun
-- NOT NULL — çağıran alanı doldurmayı unutursa insert kısıt hatasıyla düşerdi.
-- nil = "varsayılan" (oa-gateway), BOŞ liste = "çıplak pi" (bilinçli seçim);
-- ikisi ayrı şeydir, bu yüzden boş liste varsayılana çevrilmez.
INSERT INTO bots (kind, name, description, system_prompt, color_primary, color_secondary,
                  section, engine, model, temperature, allowed_tools, extensions)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
        coalesce(sqlc.narg(extensions)::text[], ARRAY['oa-gateway.ts']))
RETURNING *;

-- name: GetBot :one
SELECT * FROM bots WHERE id = $1;

-- name: ListBots :many
SELECT * FROM bots ORDER BY pinned DESC, created_at;

-- name: UpdateBot :one
-- Motor değişimi imlece dokunmaz: imleçler motor BAŞINA bot_cursors'ta yaşar
-- (ADIM 7, Ek C.2). Yeni motorun satırı ilk koşusunda doğar (boş başlar);
-- eski motorun zinciri saklanır, bota geri dönünce kaldığı yerden sürer.
--
-- extensions'ta coalesce: nil dilim SQL'de NULL'dur ve sütun NOT NULL —
-- alanı doldurmayı unutan bir çağıran botu güncelleyemez olurdu. Güncellemede
-- nil "DOKUNMA" demektir (insert'teki gibi varsayılana dönmek değil): eklenti
-- listesini bilmeyen bir çağıranın onu sessizce sıfırlaması, botun ağ geçidini
-- düşürüp bütün araçlarını kaybettirirdi. Boş liste yine bilinçli seçimdir
-- ("çıplak pi") ve aynen yazılır.
UPDATE bots SET
    name = $2, description = $3, system_prompt = $4, color_primary = $5,
    color_secondary = $6, section = $7, engine = $8, model = $9,
    temperature = $10, allowed_tools = $11, pinned = $12,
    extensions = coalesce(sqlc.narg(extensions)::text[], extensions),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteBot :exec
DELETE FROM bots WHERE id = $1;

-- name: UpsertBotCursor :exec
-- Koşu sonunda motorun döndürdüğü imleci (bot, motor, konuşma) anahtarına
-- yazar (ADIM 7+10): grup üyesi botun kendi sohbeti ile grup sohbeti ayrı
-- zincirlerdir.
INSERT INTO bot_cursors (bot_id, engine, conversation_id, cursor)
VALUES ($1, $2, $3, $4)
ON CONFLICT (bot_id, engine, conversation_id) DO UPDATE SET cursor = EXCLUDED.cursor, updated_at = now();

-- name: AddTeamMember :exec
INSERT INTO team_members (team_id, member_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (team_id, member_id) DO UPDATE SET position = EXCLUDED.position;

-- name: RemoveTeamMember :exec
DELETE FROM team_members WHERE team_id = $1 AND member_id = $2;

-- name: DeleteTeamMembers :exec
DELETE FROM team_members WHERE team_id = $1;

-- name: ListTeamMembers :many
SELECT tm.team_id, tm.position, b.*
FROM team_members tm
JOIN bots b ON b.id = tm.member_id
WHERE tm.team_id = $1
ORDER BY tm.position;

-- name: ListAllTeamMemberships :many
SELECT team_id, member_id, position FROM team_members ORDER BY team_id, position;

-- name: GetBotCursor :one
SELECT * FROM bot_cursors WHERE bot_id = $1 AND engine = $2 AND conversation_id = $3;

-- name: DeleteBotCursorsForConversation :exec
-- Sohbet temizlenince bot bağlamı da sıfırlanır: bu konuşmaya bağlı motor
-- imleçleri gider, sonraki koşu TAZE zincirle başlar.
DELETE FROM bot_cursors WHERE bot_id = $1 AND conversation_id = $2;
