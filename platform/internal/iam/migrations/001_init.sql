CREATE SCHEMA IF NOT EXISTS iam;

CREATE TABLE IF NOT EXISTS iam.departments (
    id text PRIMARY KEY,
    name varchar(100) NOT NULL,
    parent_id text REFERENCES iam.departments(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT departments_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT departments_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE TABLE IF NOT EXISTS iam.users (
    id text PRIMARY KEY,
    username varchar(50) NOT NULL,
    display_name varchar(100) NOT NULL,
    password_hash text NOT NULL,
    department_id text REFERENCES iam.departments(id),
    roles text[] NOT NULL,
    data_scope varchar(20) NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    must_change_password boolean NOT NULL DEFAULT false,
    permissions_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_normalized CHECK (username = lower(btrim(username))),
    CONSTRAINT users_roles_not_empty CHECK (cardinality(roles) > 0),
    CONSTRAINT users_data_scope_valid CHECK (data_scope IN ('own', 'department', 'descendants', 'global'))
);

CREATE UNIQUE INDEX IF NOT EXISTS users_username_unique
    ON iam.users (lower(username));

CREATE TABLE IF NOT EXISTS iam.refresh_sessions (
    token_hash text PRIMARY KEY,
    family_id text NOT NULL,
    user_id text NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS refresh_sessions_family_idx
    ON iam.refresh_sessions (family_id);

CREATE INDEX IF NOT EXISTS refresh_sessions_user_active_idx
    ON iam.refresh_sessions (user_id)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS iam.security_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type varchar(80) NOT NULL,
    user_id text,
    request_id varchar(128),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
