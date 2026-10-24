ALTER TABLE task_report.tasks ADD COLUMN IF NOT EXISTS dispatch_version bigint NOT NULL DEFAULT 0;
ALTER TABLE task_report.tasks ADD COLUMN IF NOT EXISTS next_run_at timestamptz;
ALTER TABLE task_report.tasks ADD COLUMN IF NOT EXISTS worker_id text;
ALTER TABLE task_report.tasks ADD COLUMN IF NOT EXISTS execution_token uuid;
ALTER TABLE task_report.tasks ADD COLUMN IF NOT EXISTS lease_until timestamptz;
CREATE INDEX IF NOT EXISTS tasks_lease_idx ON task_report.tasks(lease_until,id)
  WHERE state='running' AND dispatch_version>0;

ALTER TABLE task_report.outbox ADD COLUMN IF NOT EXISTS dispatch_version bigint;
ALTER TABLE task_report.outbox ADD COLUMN IF NOT EXISTS claimed_by text;
ALTER TABLE task_report.outbox ADD COLUMN IF NOT EXISTS claim_token uuid;
ALTER TABLE task_report.outbox ADD COLUMN IF NOT EXISTS claim_until timestamptz;
ALTER TABLE task_report.outbox ADD COLUMN IF NOT EXISTS superseded_at timestamptz;
CREATE UNIQUE INDEX IF NOT EXISTS outbox_task_dispatch_uidx
  ON task_report.outbox(aggregate_id,dispatch_version) WHERE topic='task.requested';
CREATE INDEX IF NOT EXISTS outbox_claim_idx ON task_report.outbox(available_at,id)
  WHERE published_at IS NULL AND superseded_at IS NULL;
ALTER TABLE task_report.inbox ADD COLUMN IF NOT EXISTS outcome text NOT NULL DEFAULT 'succeeded';

CREATE TABLE IF NOT EXISTS task_report.message_quarantine (
  id uuid PRIMARY KEY, fingerprint text NOT NULL UNIQUE,
  body text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}', reason text NOT NULL,
  quarantined_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
