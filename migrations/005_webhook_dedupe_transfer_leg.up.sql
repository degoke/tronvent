-- Align webhook_events.dedupe_key with EnqueueWebhookEvent (event type + transfer leg + endpoint).

UPDATE webhook_events e
SET dedupe_key =
  e.scope || ':' || e.tx_hash || ':' ||
  COALESCE(NULLIF(btrim(e.event_type), ''), COALESCE(e.payload->>'type', 'unknown')) || ':' ||
  COALESCE(e.payload->'data'->>'fromAddress', '') || ':' ||
  COALESCE(e.payload->'data'->>'toAddress', '') || ':' ||
  COALESCE(e.payload->'data'->>'amount', '') || ':' ||
  e.endpoint_id::text
WHERE e.endpoint_id IS NOT NULL
  AND e.dedupe_key = (e.scope || ':' || e.tx_hash || ':' || e.endpoint_id::text);

DELETE FROM webhook_events older
USING webhook_events newer
WHERE older.dedupe_key = newer.dedupe_key
  AND older.id > newer.id;
