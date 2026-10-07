ALTER TABLE webhook_endpoints
  ALTER COLUMN event_types SET DEFAULT ARRAY[
    'transaction.trx.received',
    'transaction.trx.broadcasted',
    'transaction.trc20.received',
    'transaction.trc20.broadcasted'
  ];
