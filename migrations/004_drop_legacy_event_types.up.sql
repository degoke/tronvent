-- Replace removed transaction.trx / transaction.trc20 subscription ids with direction-specific types.
UPDATE webhook_endpoints we
SET event_types = sub.new_types
FROM (
  SELECT
    id,
    array_agg(DISTINCT mapped ORDER BY mapped) AS new_types
  FROM webhook_endpoints,
  LATERAL unnest(event_types) AS t,
  LATERAL (
    SELECT unnest(
      CASE t
        WHEN 'transaction.trx' THEN ARRAY['transaction.trx.received', 'transaction.trx.broadcasted']
        WHEN 'transaction.trc20' THEN ARRAY['transaction.trc20.received', 'transaction.trc20.broadcasted']
        ELSE ARRAY[t]
      END
    ) AS mapped
  ) expanded
  GROUP BY id
) sub
WHERE we.id = sub.id
  AND we.event_types && ARRAY['transaction.trx', 'transaction.trc20']::text[];
