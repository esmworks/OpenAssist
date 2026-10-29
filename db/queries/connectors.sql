-- name: UpsertConnector :one
-- Aynı (kind, account) yeniden bağlanırsa token tazelenir, hata temizlenir.
INSERT INTO connectors (kind, account, sealed_refresh_token, scopes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kind, account) DO UPDATE SET
    sealed_refresh_token = EXCLUDED.sealed_refresh_token,
    scopes               = EXCLUDED.scopes,
    status               = 'connected',
    error                = '',
    updated_at           = now()
RETURNING *;

-- name: GetConnectorByKind :one
-- Tek kullanıcılı v1: kind başına ilk bağlı hesap kullanılır.
SELECT * FROM connectors
WHERE kind = $1 AND status = 'connected'
ORDER BY created_at
LIMIT 1;

-- name: ListConnectors :many
SELECT * FROM connectors ORDER BY created_at;

-- name: SetConnectorError :exec
-- Token yenileme kalıcı düşerse (revoke) konektör hataya çekilir; araçlar
-- kullanıcıyı yeniden bağlanmaya yönlendirir.
UPDATE connectors SET status = 'error', error = $2, updated_at = now()
WHERE id = $1;

-- name: DeleteConnector :execrows
DELETE FROM connectors WHERE id = $1;
