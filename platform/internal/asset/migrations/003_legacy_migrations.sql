CREATE TABLE IF NOT EXISTS asset.legacy_migrations (
  legacy_type text NOT NULL,
  legacy_id text NOT NULL,
  asset_id uuid NOT NULL,
  migrated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (legacy_type, legacy_id),
  UNIQUE (legacy_type, asset_id)
);
