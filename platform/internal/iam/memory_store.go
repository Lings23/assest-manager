package iam

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.Mutex
	users       map[string]User
	byName      map[string]string
	departments map[string]Department
	sessions    map[string]Session
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:       make(map[string]User),
		byName:      make(map[string]string),
		departments: make(map[string]Department),
		sessions:    make(map[string]Session),
	}
}

func (s *MemoryStore) ListDepartments(_ context.Context) ([]Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	departments := make([]Department, 0, len(s.departments))
	for _, department := range s.departments {
		departments = append(departments, department)
	}
	sort.Slice(departments, func(i, j int) bool { return departments[i].Name < departments[j].Name })
	return departments, nil
}

func (s *MemoryStore) CreateDepartment(_ context.Context, params CreateDepartmentParams) (Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.departments[params.ID]; exists {
		return Department{}, ErrConflict
	}
	if params.ParentID != "" {
		if _, exists := s.departments[params.ParentID]; !exists {
			return Department{}, ErrNotFound
		}
	}
	department := Department{ID: params.ID, Name: params.Name, ParentID: params.ParentID}
	s.departments[department.ID] = department
	return department, nil
}

func (s *MemoryStore) IsDepartmentDescendant(_ context.Context, ancestorID, candidateID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ancestorID == "" || candidateID == "" {
		return false, nil
	}
	visited := make(map[string]struct{})
	current := candidateID
	for current != "" {
		if current == ancestorID {
			return true, nil
		}
		if _, duplicate := visited[current]; duplicate {
			return false, ErrConflict
		}
		visited[current] = struct{}{}
		department, exists := s.departments[current]
		if !exists {
			return false, nil
		}
		current = department.ParentID
	}
	return false, nil
}

func (s *MemoryStore) ListDepartmentDescendantIDs(_ context.Context, ancestorID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ancestorID == "" {
		return []string{}, nil
	}
	result := []string{ancestorID}
	for {
		added := false
		for _, department := range s.departments {
			if containsString(result, department.ID) || !containsString(result, department.ParentID) {
				continue
			}
			result = append(result, department.ID)
			added = true
		}
		if !added {
			break
		}
	}
	sort.Strings(result)
	return result, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *MemoryStore) GetUserByUsername(_ context.Context, username string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byName[normalizeUsername(username)]
	if !ok {
		return User{}, ErrNotFound
	}
	return cloneUser(s.users[id]), nil
}

func (s *MemoryStore) GetUser(_ context.Context, id string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return cloneUser(user), nil
}

func (s *MemoryStore) ListUsers(_ context.Context) ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, cloneUser(user))
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, nil
}

func (s *MemoryStore) CreateUser(_ context.Context, params CreateUserParams) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username := normalizeUsername(params.Username)
	if _, exists := s.byName[username]; exists {
		return User{}, ErrConflict
	}
	now := time.Now().UTC()
	user := User{
		ID: params.ID, Username: username, DisplayName: params.DisplayName,
		PasswordHash: params.PasswordHash, DepartmentID: params.DepartmentID,
		Roles: append([]Role(nil), params.Roles...), DataScope: params.DataScope,
		Enabled: params.Enabled, MustChangePassword: params.MustChangePassword,
		PermissionsVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	s.users[user.ID] = user
	s.byName[username] = user.ID
	return cloneUser(user), nil
}

func (s *MemoryStore) UpdateUserAccess(_ context.Context, params UpdateAccessParams) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[params.UserID]
	if !ok {
		return User{}, ErrNotFound
	}
	user.Enabled = params.Enabled
	user.Roles = append([]Role(nil), params.Roles...)
	user.DataScope = params.DataScope
	user.DepartmentID = params.DepartmentID
	user.PermissionsVersion++
	user.UpdatedAt = params.UpdatedAt
	s.users[user.ID] = user
	s.revokeUserSessionsLocked(user.ID, params.UpdatedAt)
	return cloneUser(user), nil
}

func (s *MemoryStore) UpdatePassword(
	_ context.Context, userID, passwordHash string, mustChange bool, now time.Time,
) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return User{}, ErrNotFound
	}
	user.PasswordHash = passwordHash
	user.MustChangePassword = mustChange
	user.PermissionsVersion++
	user.UpdatedAt = now
	s.users[user.ID] = user
	s.revokeUserSessionsLocked(user.ID, now)
	return cloneUser(user), nil
}

func (s *MemoryStore) CreateSession(_ context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.TokenHash]; exists {
		return ErrConflict
	}
	s.sessions[session.TokenHash] = session
	return nil
}

func (s *MemoryStore) RotateSession(_ context.Context, oldHash string, next Session, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[oldHash]
	if !ok {
		return Session{}, ErrUnauthorized
	}
	if current.UsedAt != nil {
		s.revokeFamilyLocked(current.FamilyID, now)
		return Session{}, ErrRefreshReuse
	}
	if current.RevokedAt != nil || !current.ExpiresAt.After(now) {
		return Session{}, ErrUnauthorized
	}
	current.UsedAt = &now
	s.sessions[oldHash] = current
	next.FamilyID = current.FamilyID
	next.UserID = current.UserID
	s.sessions[next.TokenHash] = next
	return current, nil
}

func (s *MemoryStore) RevokeSession(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[tokenHash]
	if !ok {
		return nil
	}
	s.revokeFamilyLocked(session.FamilyID, now)
	return nil
}

func (s *MemoryStore) RevokeUserSessions(_ context.Context, userID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokeUserSessionsLocked(userID, now)
	return nil
}

func (s *MemoryStore) Close() {}

func (s *MemoryStore) revokeFamilyLocked(familyID string, now time.Time) {
	for hash, session := range s.sessions {
		if session.FamilyID == familyID && session.RevokedAt == nil {
			session.RevokedAt = &now
			s.sessions[hash] = session
		}
	}
}

func (s *MemoryStore) revokeUserSessionsLocked(userID string, now time.Time) {
	for hash, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
			s.sessions[hash] = session
		}
	}
}

func cloneUser(user User) User {
	user.Roles = append([]Role(nil), user.Roles...)
	return user
}
