-- name: RecordWebhookEvent :execrows
-- 0 satır = çift teslimat, olay işlenmez (§6).
INSERT INTO webhook_events (source, event_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;
