-- name: CreateRun :one
-- reply_to_run_id: botlar arası devirde kaynak koşu (ADIM 6); boşsa NULL.
-- group_hop: grup kanalındaki @mention zincirinin derinliği (ADIM 10 ikinci
-- aşama); kullanıcıdan gelen mesaj 0, her üye→üye devri bir artırır.
INSERT INTO runs (bot_id, prompt, engine, model, resume_cursor, grant_decision, pending_ask, origin, reply_to_run_id, conversation_id, group_hop)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetRun :one
SELECT * FROM runs WHERE id = $1;

-- name: ListRunsByBot :many
SELECT * FROM runs WHERE bot_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListRunsByStatus :many
SELECT * FROM runs WHERE status = $1 ORDER BY created_at DESC LIMIT $2;

-- name: ListRecentRuns :many
-- Aktivite akışı (K19): tüm durumlar, yeniden eskiye.
-- Arka plan bakım turları (gece hafıza konsolidasyonu) DIŞARIDA: her gece her
-- bot için akışa bir satır düşseydi kullanıcının kendi işleri görünmez olurdu.
-- Maliyet defterindeki (RunStats) yerleri duruyor — görünmezlik yalnız akışta.
SELECT * FROM runs WHERE origin <> 'consolidation' ORDER BY queued_at DESC LIMIT $1;

-- name: RecordBackgroundRun :one
-- Arka plan bakım turunun maliyet defteri satırı (K20: konsolidasyon + koşu
-- sonrası çıkarım, ADIM 8). origin çağırandan gelir (consolidation|inference).
--
-- Tur BİTTİKTEN SONRA doğrudan terminal durumda açılır. 'queued' açılsaydı
-- ClaimNextRun onu normal sohbet koşusu sanıp yürütürdü; 'running' bırakılsaydı
-- süreç çökmesinde sonsuza kadar öyle kalırdı (kalp atışı yok, RequeueStaleRuns
-- NULL heartbeat'i görmez). Amaç kuyruğa girmek değil, harcanan parayı
-- /api/stats'ın görebileceği tek yere yazmak.
INSERT INTO runs (bot_id, prompt, engine, model, origin, status,
                  started_at, finished_at, cost_usd, input_tokens, output_tokens,
                  num_turns, final_text, error)
VALUES ($1, $2, $3, $4, $11, sqlc.arg(status),
        sqlc.arg(started_at), now(), $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: RunStats :one
-- Maliyet panosu (Faz 9): son N günün toplamları.
SELECT count(*)                                          AS total_runs,
       count(*) FILTER (WHERE status = 'completed')      AS completed,
       count(*) FILTER (WHERE status = 'failed')         AS failed,
       count(*) FILTER (WHERE status = 'expired')        AS expired,
       count(*) FILTER (WHERE status = 'approval_pending') AS pending,
       COALESCE(sum(cost_usd), 0)::float8                AS total_cost_usd,
       COALESCE(sum(input_tokens), 0)::bigint            AS input_tokens,
       COALESCE(sum(output_tokens), 0)::bigint           AS output_tokens
FROM runs
WHERE queued_at > now() - (sqlc.arg(days)::int * interval '1 day');

-- name: RunStatsByBot :many
SELECT bot_id,
       count(*)                           AS runs,
       COALESCE(sum(cost_usd), 0)::float8 AS cost_usd
FROM runs
WHERE queued_at > now() - (sqlc.arg(days)::int * interval '1 day')
GROUP BY bot_id
ORDER BY cost_usd DESC;

-- name: ListRunsByBotAndStatus :many
-- Telegram: botun bekleyen soru koşusunu bulmak için (K16).
SELECT * FROM runs WHERE bot_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3;

-- name: ClaimNextRun :one
UPDATE runs SET status = 'running', claimed_by = $1,
                started_at = now(), heartbeat_at = now(), attempt = attempt + 1
WHERE id = (
    SELECT id FROM runs WHERE status = 'queued'
    ORDER BY queued_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: HeartbeatRun :exec
UPDATE runs SET heartbeat_at = now() WHERE id = $1 AND status = 'running';

-- name: CompleteRun :exec
UPDATE runs SET status = 'completed', finished_at = now(), final_text = $2,
                result_cursor = $3, cost_usd = $4, input_tokens = $5,
                output_tokens = $6, num_turns = $7
WHERE id = $1;

-- name: FailRun :exec
UPDATE runs SET status = 'failed', finished_at = now(), error = $2 WHERE id = $1;

-- name: ParkRun :exec
UPDATE runs SET status = 'approval_pending', finished_at = now(), pending_ask = $2,
                result_cursor = $3, cost_usd = $4, input_tokens = $5,
                output_tokens = $6, num_turns = $7, final_text = $8,
                grant_decision = ''
WHERE id = $1;

-- name: ResolveApproval :one
-- Park edilmiş koşuyu kararla yeniden kuyruğa alır (resume). Yalnızca
-- approval_pending durumundayken çalışır — yarış koşulu DB'de çözülür.
UPDATE runs SET status = 'queued', grant_decision = $2, prompt = $3,
                resume_cursor = result_cursor, queued_at = now(),
                claimed_by = '', heartbeat_at = NULL,
                started_at = NULL, finished_at = NULL
WHERE id = $1 AND status = 'approval_pending'
RETURNING *;

-- name: SetRunTainted :exec
UPDATE runs SET tainted = true WHERE id = $1;

-- name: ExpireStaleApprovals :execrows
-- Park süresi (TTL) dolan onay bekleyen koşular 'expired' olur (K14).
UPDATE runs SET status = 'expired'
WHERE status = 'approval_pending'
  AND finished_at < now() - make_interval(secs => sqlc.arg(ttl_secs)::double precision);

-- name: RequeueStaleRuns :execrows
-- Kalp atışı kesilen koşuları (çökmüş worker/deploy) kuyruğa geri koyar.
UPDATE runs SET status = 'queued', claimed_by = '', heartbeat_at = NULL, started_at = NULL
WHERE status = 'running'
  AND heartbeat_at < now() - make_interval(secs => sqlc.arg(stale_secs)::double precision)
  AND attempt < sqlc.arg(max_attempts)::int;

-- name: FailStaleRuns :many
-- Deneme hakkı biten sahipsiz koşular hata durumuna düşer.
-- id DÖNER: çağıran her koşu için sohbete hata çipi yazar ve olay yayınlar —
-- sayı yetmiyordu, kullanıcı cevabı gelmeyen botun neden sustuğunu göremiyordu.
UPDATE runs SET status = 'failed', finished_at = now(),
                error = 'koşu sahipsiz kaldı ve deneme hakkı bitti (kalp atışı kesildi)'
WHERE status = 'running'
  AND heartbeat_at < now() - make_interval(secs => sqlc.arg(stale_secs)::double precision)
  AND attempt >= sqlc.arg(max_attempts)::int
RETURNING id;
