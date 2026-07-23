CREATE SCHEMA IF NOT EXISTS iam;
CREATE SCHEMA IF NOT EXISTS asset;
CREATE SCHEMA IF NOT EXISTS governance;
CREATE SCHEMA IF NOT EXISTS task_report;

COMMENT ON SCHEMA iam IS 'Owned exclusively by iam-service';
COMMENT ON SCHEMA asset IS 'Owned exclusively by asset-service';
COMMENT ON SCHEMA governance IS 'Owned exclusively by governance-service';
COMMENT ON SCHEMA task_report IS 'Owned exclusively by task-report-service';
