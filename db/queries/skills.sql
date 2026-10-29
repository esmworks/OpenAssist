-- name: UpsertSkill :one
INSERT INTO skills (name, when_to_use, instructions)
VALUES ($1, $2, $3)
ON CONFLICT (name)
    DO UPDATE SET when_to_use = EXCLUDED.when_to_use,
                  instructions = EXCLUDED.instructions, updated_at = now()
RETURNING *;

-- name: ListSkills :many
SELECT * FROM skills ORDER BY name;

-- name: GetSkillByName :one
SELECT * FROM skills WHERE name = $1;

-- name: DeleteSkill :execrows
DELETE FROM skills WHERE id = $1;

-- name: EnableSkillForBot :exec
INSERT INTO bot_skills (bot_id, skill_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DisableSkillForBot :execrows
DELETE FROM bot_skills WHERE bot_id = $1 AND skill_id = $2;

-- name: ListSkillsForBot :many
-- Botun etkin skill'leri sistem promptuna girer (K11: NASIL bilgisi).
SELECT s.* FROM skills s
JOIN bot_skills bs ON bs.skill_id = s.id
WHERE bs.bot_id = $1
ORDER BY s.name;
