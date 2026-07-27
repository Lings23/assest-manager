package iam

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_init.sql
var iamMigration string

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if _, err := pool.Exec(ctx, iamMigration); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply IAM migration: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) GetUserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT id, username, display_name, password_hash, COALESCE(department_id, ''),
		       roles, data_scope, enabled, must_change_password, permissions_version,
		       created_at, updated_at
		FROM iam.users
		WHERE username = lower(btrim($1))
	`, username))
}

func (s *PostgresStore) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		SELECT id, username, display_name, password_hash, COALESCE(department_id, ''),
		       roles, data_scope, enabled, must_change_password, permissions_version,
		       created_at, updated_at
		FROM iam.users
		WHERE id = $1
	`, id))
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, username, display_name, password_hash, COALESCE(department_id, ''),
		       roles, data_scope, enabled, must_change_password, permissions_version,
		       created_at, updated_at
		FROM iam.users
		ORDER BY username
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *PostgresStore) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	roleValues := rolesToStrings(params.Roles)
	user, err := scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO iam.users (
			id, username, display_name, password_hash, department_id, roles,
			data_scope, enabled, must_change_password
		)
		VALUES ($1, lower(btrim($2)), $3, $4, NULLIF($5, ''), $6, $7, $8, $9)
		RETURNING id, username, display_name, password_hash, COALESCE(department_id, ''),
		          roles, data_scope, enabled, must_change_password, permissions_version,
		          created_at, updated_at
	`, params.ID, params.Username, params.DisplayName, params.PasswordHash,
		params.DepartmentID, roleValues, string(params.DataScope),
		params.Enabled, params.MustChangePassword))
	if isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return user, err
}

func (s *PostgresStore) UpdateUserAccess(ctx context.Context, params UpdateAccessParams) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := scanUser(tx.QueryRow(ctx, `
		UPDATE iam.users
		SET enabled = $2, roles = $3, data_scope = $4, department_id = NULLIF($5, ''),
		    permissions_version = permissions_version + 1, updated_at = $6
		WHERE id = $1
		RETURNING id, username, display_name, password_hash, COALESCE(department_id, ''),
		          roles, data_scope, enabled, must_change_password, permissions_version,
		          created_at, updated_at
	`, params.UserID, params.Enabled, rolesToStrings(params.Roles), string(params.DataScope),
		params.DepartmentID, params.UpdatedAt))
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE iam.refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, params.UserID, params.UpdatedAt); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) UpdatePassword(
	ctx context.Context, userID, passwordHash string, mustChange bool, now time.Time,
) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := scanUser(tx.QueryRow(ctx, `
		UPDATE iam.users
		SET password_hash = $2, must_change_password = $3,
		    permissions_version = permissions_version + 1, updated_at = $4
		WHERE id = $1
		RETURNING id, username, display_name, password_hash, COALESCE(department_id, ''),
		          roles, data_scope, enabled, must_change_password, permissions_version,
		          created_at, updated_at
	`, userID, passwordHash, mustChange, now))
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE iam.refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *PostgresStore) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(parent_id, '')
		FROM iam.departments
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	departments := make([]Department, 0)
	for rows.Next() {
		var department Department
		if err := rows.Scan(&department.ID, &department.Name, &department.ParentID); err != nil {
			return nil, err
		}
		departments = append(departments, department)
	}
	return departments, rows.Err()
}

func (s *PostgresStore) CreateDepartment(
	ctx context.Context, params CreateDepartmentParams,
) (Department, error) {
	var department Department
	err := s.pool.QueryRow(ctx, `
		INSERT INTO iam.departments (id, name, parent_id)
		VALUES ($1, $2, NULLIF($3, ''))
		RETURNING id, name, COALESCE(parent_id, '')
	`, params.ID, params.Name, params.ParentID).Scan(
		&department.ID, &department.Name, &department.ParentID,
	)
	if isUniqueViolation(err) {
		return Department{}, ErrConflict
	}
	if isForeignKeyViolation(err) {
		return Department{}, ErrNotFound
	}
	return department, err
}

func (s *PostgresStore) IsDepartmentDescendant(
	ctx context.Context, ancestorID, candidateID string,
) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, parent_id
			FROM iam.departments
			WHERE id = $2
			UNION ALL
			SELECT parent.id, parent.parent_id
			FROM iam.departments parent
			JOIN ancestors child ON child.parent_id = parent.id
		)
		SELECT EXISTS(SELECT 1 FROM ancestors WHERE id = $1)
	`, ancestorID, candidateID).Scan(&allowed)
	return allowed, err
}

func (s *PostgresStore) ListDepartmentDescendantIDs(
	ctx context.Context, ancestorID string,
) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendants AS (
			SELECT id FROM iam.departments WHERE id = $1
			UNION ALL
			SELECT child.id
			FROM iam.departments child
			JOIN descendants parent ON child.parent_id = parent.id
		)
		SELECT id FROM descendants ORDER BY id
	`, ancestorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) CreateSession(ctx context.Context, session Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO iam.refresh_sessions (
			token_hash, family_id, user_id, expires_at, used_at, revoked_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, session.TokenHash, session.FamilyID, session.UserID, session.ExpiresAt,
		session.UsedAt, session.RevokedAt, session.CreatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (s *PostgresStore) RotateSession(
	ctx context.Context, oldHash string, next Session, now time.Time,
) (Session, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current Session
	err = tx.QueryRow(ctx, `
		SELECT token_hash, family_id, user_id, expires_at, used_at, revoked_at, created_at
		FROM iam.refresh_sessions
		WHERE token_hash = $1
		FOR UPDATE
	`, oldHash).Scan(
		&current.TokenHash, &current.FamilyID, &current.UserID, &current.ExpiresAt,
		&current.UsedAt, &current.RevokedAt, &current.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, err
	}
	if current.UsedAt != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE iam.refresh_sessions
			SET revoked_at = COALESCE(revoked_at, $2)
			WHERE family_id = $1
		`, current.FamilyID, now); err != nil {
			return Session{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Session{}, err
		}
		return Session{}, ErrRefreshReuse
	}
	if current.RevokedAt != nil || !current.ExpiresAt.After(now) {
		return Session{}, ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		UPDATE iam.refresh_sessions SET used_at = $2 WHERE token_hash = $1
	`, oldHash, now); err != nil {
		return Session{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO iam.refresh_sessions (
			token_hash, family_id, user_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`, next.TokenHash, current.FamilyID, current.UserID, next.ExpiresAt, next.CreatedAt); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return current, nil
}

func (s *PostgresStore) RevokeSession(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE iam.refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE family_id = (
			SELECT family_id FROM iam.refresh_sessions WHERE token_hash = $1
		) AND revoked_at IS NULL
	`, tokenHash, now)
	return err
}

func (s *PostgresStore) RevokeUserSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE iam.refresh_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, now)
	return err
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

type rowScanner interface {
	Scan(...any) error
}

func scanUser(row rowScanner) (User, error) {
	var user User
	var roles []string
	var scope string
	err := row.Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.PasswordHash, &user.DepartmentID,
		&roles, &scope, &user.Enabled, &user.MustChangePassword, &user.PermissionsVersion,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	user.Roles = stringsToRoles(roles)
	user.DataScope = DataScope(scope)
	return user, nil
}

func rolesToStrings(roles []Role) []string {
	result := make([]string, len(roles))
	for index, role := range roles {
		result[index] = string(role)
	}
	return result
}

func stringsToRoles(values []string) []Role {
	result := make([]Role, len(values))
	for index, value := range values {
		result[index] = Role(value)
	}
	return result
}

type sqlStateError interface {
	SQLState() string
}

func isUniqueViolation(err error) bool {
	var state sqlStateError
	return errors.As(err, &state) && state.SQLState() == "23505"
}

func isForeignKeyViolation(err error) bool {
	var state sqlStateError
	return errors.As(err, &state) && state.SQLState() == "23503"
}
