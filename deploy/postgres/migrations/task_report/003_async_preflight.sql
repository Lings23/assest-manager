-- Stop old API processes before this migration. Confirmed executions are untouched.
-- Recover interrupted generation-zero preflights once, via the normal leased worker.
WITH recovered AS (
 UPDATE task_report.tasks SET input=input||'{"phase":"preflight"}'::jsonb,
 state='pending',attempt=0,progress=0,dispatch_version=1,next_run_at=clock_timestamp(),
 worker_id=NULL,execution_token=NULL,lease_until=NULL,updated_at=clock_timestamp()
 WHERE kind='import' AND dispatch_version=0 AND state IN ('pending','running','failed')
 AND NOT result @> '{"confirmed":true}'::jsonb
 RETURNING *
), events AS (
 SELECT gen_random_uuid() AS event_id,id,kind,dispatch_version,next_run_at FROM recovered
)
INSERT INTO task_report.outbox(event_id,topic,aggregate_id,dispatch_version,payload,available_at)
SELECT event_id,'task.requested',id,dispatch_version,
 jsonb_build_object('schema_version',2,'event_id',event_id,'task_id',id,'kind',kind,'dispatch_version',dispatch_version),next_run_at
FROM events ON CONFLICT DO NOTHING;
