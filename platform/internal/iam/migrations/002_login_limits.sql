CREATE TABLE IF NOT EXISTS iam.login_limits (
    key_hash text PRIMARY KEY,
    opened_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL CHECK(attempts >= 0)
);
CREATE INDEX IF NOT EXISTS login_limits_expiry_idx ON iam.login_limits(expires_at);
