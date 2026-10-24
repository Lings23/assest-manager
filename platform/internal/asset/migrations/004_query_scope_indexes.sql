-- Explicit performance migration; not executed by asset-service startup.
-- Run with writers stopped. Repeatable; existing generated indexes are retained.
SET lock_timeout = '5s';
SET statement_timeout = '5min';
DO $migration$
DECLARE asset_table text;
BEGIN
    FOREACH asset_table IN ARRAY ARRAY[
        'data_assets', 'hardware_software_assets', 'responsible_department_assets',
        'software_statistics_assets', 'supply_chain_assets', 'system_info_assets',
        'vulnerability_assets'
    ] LOOP
        EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON asset.%I (updated_at DESC, id ASC) WHERE deleted_at IS NULL',
                       asset_table || '_live_order_idx', asset_table);
        EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON asset.%I (owner_id, updated_at DESC, id ASC) WHERE deleted_at IS NULL',
                       asset_table || '_live_owner_order_idx', asset_table);
        EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON asset.%I (department_id, updated_at DESC, id ASC) WHERE deleted_at IS NULL',
                       asset_table || '_live_dept_order_idx', asset_table);
    END LOOP;
END;
$migration$;
