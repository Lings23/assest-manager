-- Independent from 004: evaluate substring search and its write/storage cost.
-- The D1 primary HTTP search workload targets the largest Responsible table.
-- Keep the existing OR/ILIKE predicates; no similarity-based semantic change.
SET lock_timeout = '5s';
SET statement_timeout = '5min';
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;
DO $migration$
DECLARE operator_schema text;
BEGIN
    SELECT n.nspname INTO operator_schema
      FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace
      WHERE e.extname='pg_trgm';
    IF operator_schema IS NULL THEN
        RAISE EXCEPTION 'pg_trgm is unavailable';
    END IF;
    -- Qualify the opclass using its actual schema, including existing installs.
    EXECUTE format(
        'CREATE INDEX IF NOT EXISTS %I ON asset.%I USING gin
        ((department_name::text) %I.gin_trgm_ops,
         (department_code::text) %I.gin_trgm_ops,
         (department_head::text) %I.gin_trgm_ops)
        WHERE deleted_at IS NULL',
        'responsible_department_assets_live_search_trgm_idx',
        'responsible_department_assets', operator_schema, operator_schema, operator_schema);
END;
$migration$;
