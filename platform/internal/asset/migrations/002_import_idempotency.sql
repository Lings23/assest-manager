CREATE TABLE IF NOT EXISTS asset.import_idempotency (
  task_id uuid NOT NULL,
  row_key text NOT NULL,
  asset_type text NOT NULL,
  asset_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, row_key),
  UNIQUE (asset_type, asset_id)
);
