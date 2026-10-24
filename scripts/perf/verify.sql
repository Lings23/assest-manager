-- psql -v task_ids_json='["uuid",...]' -f verify.sql
-- This file intentionally performs no writes and limits every lookup to supplied IDs.
\set ON_ERROR_STOP on
BEGIN READ ONLY;
SET LOCAL statement_timeout = '30s';
WITH wanted AS (
  SELECT jsonb_array_elements_text(:'task_ids_json'::jsonb)::uuid AS id
), records AS (
  SELECT 'data' AS asset_type, id, version, deleted_at FROM asset.data_assets
  UNION ALL SELECT 'hardware-software', id, version, deleted_at FROM asset.hardware_software_assets
  UNION ALL SELECT 'responsible-department', id, version, deleted_at FROM asset.responsible_department_assets
  UNION ALL SELECT 'software-statistics', id, version, deleted_at FROM asset.software_statistics_assets
  UNION ALL SELECT 'supply-chain', id, version, deleted_at FROM asset.supply_chain_assets
  UNION ALL SELECT 'system-info', id, version, deleted_at FROM asset.system_info_assets
  UNION ALL SELECT 'vulnerability', id, version, deleted_at FROM asset.vulnerability_assets
)
SELECT w.id::text AS task_id, t.kind, t.asset_type, t.state, t.attempt,
       COALESCE((t.result->>'committed')::bigint,0) AS reported_committed,
       COALESCE((t.result->>'created')::bigint,0) AS reported_created,
       COALESCE((t.result->>'skipped')::bigint,0) AS reported_skipped,
       (SELECT count(*) FROM asset.import_idempotency i WHERE i.task_id=w.id) AS import_keys,
       (SELECT count(*) FROM task_report.import_rows r WHERE r.task_id=w.id) AS parsed_rows,
       (SELECT count(*) FROM task_report.import_rows r WHERE r.task_id=w.id AND r.committed_at IS NOT NULL) AS committed_rows,
       (SELECT count(*) FROM asset.import_idempotency i JOIN records a ON a.asset_type=i.asset_type AND a.id=i.asset_id::text WHERE i.task_id=w.id AND a.deleted_at IS NULL) AS active_assets,
       (SELECT count(*) FROM asset.import_idempotency i JOIN asset.asset_versions v ON v.asset_type=i.asset_type AND v.asset_id=i.asset_id::text WHERE i.task_id=w.id AND v.version=1 AND v.action='import') AS import_versions,
       (SELECT count(*) FROM task_report.tasks d WHERE d.requested_by=t.requested_by AND d.kind=t.kind AND d.idempotency_key=t.idempotency_key) AS same_key_tasks,
       (SELECT count(*) FROM task_report.outbox o WHERE o.aggregate_id=w.id AND o.published_at IS NULL) AS unpublished_events,
       (SELECT count(*) FROM task_report.dead_letters d WHERE d.task_id=w.id) AS dead_letters
FROM wanted w LEFT JOIN task_report.tasks t ON t.id=w.id ORDER BY w.id;
COMMIT;
