-- name: ListProviders :many
SELECT * FROM providers ORDER BY position, id;

-- name: ListEnabledProviders :many
-- Koşu yolunda kullanılır: models.json yalnız etkin sağlayıcıları taşır.
SELECT * FROM providers WHERE enabled ORDER BY position, id;

-- name: GetProvider :one
SELECT * FROM providers WHERE id = $1;

-- name: CountProviders :one
SELECT count(*) FROM providers;

-- name: UpsertProvider :one
-- Anahtar AYRI güncellenir (SetProviderKey): burada boş geçmek "anahtara
-- dokunma" demektir. Aksi halde her düzenlemede istemcinin anahtarı geri
-- göndermesi gerekirdi ve anahtar hiç göndermediğimiz bir değer.
INSERT INTO providers (id, name, base_url, api, enabled, compat, position)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name, base_url = EXCLUDED.base_url, api = EXCLUDED.api,
    enabled = EXCLUDED.enabled, compat = EXCLUDED.compat,
    position = EXCLUDED.position, updated_at = now()
RETURNING *;

-- name: SetProviderKey :exec
UPDATE providers SET sealed_api_key = $2, updated_at = now() WHERE id = $1;

-- name: DeleteProvider :exec
DELETE FROM providers WHERE id = $1;

-- name: ListProviderModels :many
SELECT * FROM provider_models ORDER BY provider_id, position, model_id;

-- name: ListModelsForProvider :many
SELECT * FROM provider_models WHERE provider_id = $1 ORDER BY position, model_id;

-- name: UpsertProviderModel :exec
INSERT INTO provider_models (provider_id, model_id, label, context_window, max_tokens, reasoning, input, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (provider_id, model_id) DO UPDATE SET
    label = EXCLUDED.label, context_window = EXCLUDED.context_window,
    max_tokens = EXCLUDED.max_tokens, reasoning = EXCLUDED.reasoning,
    input = EXCLUDED.input, position = EXCLUDED.position;

-- name: DeleteProviderModels :exec
DELETE FROM provider_models WHERE provider_id = $1;

-- name: DeleteProviderModel :exec
DELETE FROM provider_models WHERE provider_id = $1 AND model_id = $2;
