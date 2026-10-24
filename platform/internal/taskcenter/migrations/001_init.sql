CREATE SCHEMA IF NOT EXISTS task_report;

CREATE TABLE IF NOT EXISTS task_report.tasks (
  id uuid PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('import','export','migration','report_refresh')),
  asset_type text NOT NULL,
  state text NOT NULL CHECK (state IN ('pending','running','awaiting_confirmation','succeeded','failed','cancelled','dead_letter')),
  idempotency_key text NOT NULL,
  requested_by uuid NOT NULL,
  department_id uuid,
  progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
  attempt integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 10),
  input jsonb NOT NULL DEFAULT '{}', result jsonb NOT NULL DEFAULT '{}', last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (requested_by, kind, idempotency_key)
);
CREATE INDEX IF NOT EXISTS tasks_claim_idx ON task_report.tasks (state, created_at);

CREATE TABLE IF NOT EXISTS task_report.outbox (
  id bigserial PRIMARY KEY, event_id uuid NOT NULL UNIQUE, topic text NOT NULL,
  aggregate_id uuid NOT NULL, payload jsonb NOT NULL, attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz, last_error text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbox_dispatch_idx ON task_report.outbox (available_at) WHERE published_at IS NULL;
CREATE TABLE IF NOT EXISTS task_report.inbox (
  consumer text NOT NULL, event_id uuid NOT NULL, processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, event_id)
);
CREATE TABLE IF NOT EXISTS task_report.dead_letters (
  task_id uuid PRIMARY KEY REFERENCES task_report.tasks(id), payload jsonb NOT NULL,
  reason text NOT NULL, failed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS task_report.import_rows (
  task_id uuid NOT NULL REFERENCES task_report.tasks(id) ON DELETE CASCADE, row_number integer NOT NULL,
  row_key text NOT NULL, payload jsonb NOT NULL, error text NOT NULL DEFAULT '', committed_at timestamptz,
  PRIMARY KEY (task_id, row_number), UNIQUE(task_id, row_key)
);
CREATE TABLE IF NOT EXISTS task_report.reporting_counts (
  asset_type text NOT NULL, department_id text NOT NULL DEFAULT '', visible_scope text NOT NULL,
  total bigint NOT NULL, refreshed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(asset_type, department_id, visible_scope)
);
CREATE TABLE IF NOT EXISTS task_report.permission_counts (
  asset_type text NOT NULL, requester_id uuid NOT NULL, scope_key text NOT NULL,
  total bigint NOT NULL, refreshed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(asset_type, requester_id, scope_key)
);

CREATE TABLE IF NOT EXISTS task_report.legacy_migrations (
  legacy_type text NOT NULL, legacy_id text NOT NULL, asset_id uuid NOT NULL,
  migrated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(legacy_type, legacy_id)
);
