-- Controlled rollback of 004/005 only. Stop writers before using this script.
-- Retain pg_trgm: other indexes/services might depend on the installed extension.
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
        EXECUTE format('DROP INDEX IF EXISTS asset.%I',asset_table || '_live_order_idx');
        EXECUTE format('DROP INDEX IF EXISTS asset.%I',asset_table || '_live_owner_order_idx');
        EXECUTE format('DROP INDEX IF EXISTS asset.%I',asset_table || '_live_dept_order_idx');
    END LOOP;
    DROP INDEX IF EXISTS asset.responsible_department_assets_live_search_trgm_idx;
END;
$migration$;
