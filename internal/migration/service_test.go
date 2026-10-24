package migration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

type memoryRepository struct {
	mu         sync.Mutex
	nextID     int64
	migrations map[int64]migration.Migration
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		nextID:     1,
		migrations: make(map[int64]migration.Migration),
	}
}

func (r *memoryRepository) CreateDraft(_ context.Context, p migration.CreateDraftParams) (migration.Migration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := r.nextID
	r.nextID++
	m := migration.Migration{
		ID:         id,
		DatabaseID: p.DatabaseID,
		Title:      p.Title,
		SQL:        p.SQL,
		Status:     migration.StatusDraft,
		CreatedBy:  p.CreatedBy,
		CreatedAt:  time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
	}
	r.migrations[id] = m
	return m, nil
}

func (r *memoryRepository) UpdateDraft(_ context.Context, p migration.UpdateDraftParams) (migration.Migration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, ok := r.migrations[p.ID]
	if !ok || m.DatabaseID != p.DatabaseID {
		return migration.Migration{}, migration.ErrNotFound
	}
	if m.Status == migration.StatusApplied {
		return migration.Migration{}, migration.ErrAlreadyApplied
	}
	if m.Status != migration.StatusDraft {
		return migration.Migration{}, migration.ErrNotDraft
	}
	m.Title = p.Title
	m.SQL = p.SQL
	r.migrations[p.ID] = m
	return m, nil
}

func (r *memoryRepository) GetByID(_ context.Context, databaseID, migrationID int64) (migration.Migration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, ok := r.migrations[migrationID]
	if !ok || m.DatabaseID != databaseID {
		return migration.Migration{}, migration.ErrNotFound
	}
	return m, nil
}

func (r *memoryRepository) ListByDatabase(_ context.Context, databaseID int64) ([]migration.Migration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []migration.Migration
	for _, m := range r.migrations {
		if m.DatabaseID == databaseID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *memoryRepository) NextVersion(_ context.Context, databaseID int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	maxVer := 0
	for _, m := range r.migrations {
		if m.DatabaseID == databaseID && m.Version != nil && *m.Version > maxVer {
			maxVer = *m.Version
		}
	}
	return maxVer + 1, nil
}

func (r *memoryRepository) RecordExecution(_ context.Context, p migration.RecordExecutionParams) (migration.Migration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !migration.StatusDraft.CanTransitionTo(p.Status) {
		return migration.Migration{}, migration.ErrInvalidTransition
	}
	m, ok := r.migrations[p.ID]
	if !ok || m.DatabaseID != p.DatabaseID {
		return migration.Migration{}, migration.ErrNotFound
	}
	if m.Status == migration.StatusApplied {
		return migration.Migration{}, migration.ErrAlreadyApplied
	}
	if m.Status != migration.StatusDraft {
		return migration.Migration{}, migration.ErrNotDraft
	}

	for _, existing := range r.migrations {
		if existing.DatabaseID == p.DatabaseID && existing.Version != nil && *existing.Version == p.Version {
			return migration.Migration{}, migration.ErrVersionConflict
		}
	}

	ver := p.Version
	execBy := p.ExecutedBy
	execAt := p.ExecutedAt
	m.Version = &ver
	m.Status = p.Status
	m.ExecutedBy = &execBy
	m.ExecutedAt = &execAt
	m.ErrorMessage = p.ErrorMessage
	r.migrations[p.ID] = m
	return m, nil
}

type noopLocker struct {
	mu sync.Mutex
}

func (l *noopLocker) Acquire(_ context.Context, _ int64) (func() error, error) {
	l.mu.Lock()
	return func() error {
		l.mu.Unlock()
		return nil
	}, nil
}

type stubExecutor struct {
	err   error
	calls int
}

func (e *stubExecutor) ExecInTx(_ context.Context, _ int64, _ string) error {
	e.calls++
	return e.err
}

type stubChecker struct {
	grants map[[2]int64]migration.AccessLevel
}

func (c *stubChecker) EffectiveLevel(_ context.Context, userID, databaseID int64) (migration.AccessLevel, error) {
	return c.grants[[2]int64{userID, databaseID}], nil
}

func TestStatusStateMachine(t *testing.T) {
	if !migration.StatusDraft.IsValid() || !migration.StatusApplied.IsValid() || !migration.StatusFailed.IsValid() {
		t.Fatal("expected draft, applied, failed to be valid statuses")
	}
	if migration.Status("unknown").IsValid() {
		t.Fatal("expected unknown status to be invalid")
	}
	if !migration.StatusDraft.CanTransitionTo(migration.StatusApplied) {
		t.Error("draft should transition to applied")
	}
	if !migration.StatusDraft.CanTransitionTo(migration.StatusFailed) {
		t.Error("draft should transition to failed")
	}
	if migration.StatusDraft.CanTransitionTo(migration.StatusDraft) {
		t.Error("draft should not transition to draft")
	}
	if migration.StatusApplied.CanTransitionTo(migration.StatusFailed) || migration.StatusApplied.CanTransitionTo(migration.StatusApplied) {
		t.Error("applied is terminal and must not transition")
	}
	if migration.StatusFailed.CanTransitionTo(migration.StatusApplied) || migration.StatusFailed.CanTransitionTo(migration.StatusFailed) {
		t.Error("failed is terminal and must not transition")
	}
}

func TestServiceAuthorization(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	locker := &noopLocker{}
	exec := &stubExecutor{}
	checker := &stubChecker{
		grants: map[[2]int64]migration.AccessLevel{
			{1, 100}: migration.LevelNone,
			{2, 100}: migration.LevelView,
			{3, 100}: migration.LevelEdit,
		},
	}
	runner := migration.NewRunner(repo, locker, exec)
	svc := migration.NewService(repo, runner, checker)

	if _, err := svc.ListHistory(ctx, 0, 100); !errors.Is(err, migration.ErrUnauthenticated) {
		t.Fatalf("unauthenticated ListHistory: got %v, want ErrUnauthenticated", err)
	}

	if _, err := svc.ListHistory(ctx, 1, 100); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelNone ListHistory: got %v, want ErrForbidden", err)
	}
	if _, err := svc.CreateDraft(ctx, 1, 100, "T", "CREATE TABLE t (id int);"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelNone CreateDraft: got %v, want ErrForbidden", err)
	}

	draft, err := svc.CreateDraft(ctx, 3, 100, "Create users", "CREATE TABLE users (id int);")
	if err != nil {
		t.Fatalf("LevelEdit CreateDraft failed: %v", err)
	}

	if list, err := svc.ListHistory(ctx, 2, 100); err != nil || len(list) != 1 {
		t.Fatalf("LevelView ListHistory: got len=%d, err=%v", len(list), err)
	}
	if got, err := svc.GetDetail(ctx, 2, 100, draft.ID); err != nil || got.ID != draft.ID {
		t.Fatalf("LevelView GetDetail: got %+v, err=%v", got, err)
	}

	if _, err := svc.CreateDraft(ctx, 2, 100, "Hack", "DROP TABLE users;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelView CreateDraft: got %v, want ErrForbidden", err)
	}
	if _, err := svc.UpdateDraft(ctx, 2, 100, draft.ID, "Hack", "DROP TABLE users;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelView UpdateDraft: got %v, want ErrForbidden", err)
	}
	if _, err := svc.PreviewSQL(ctx, 2, 100, "Hack", "SELECT 1;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelView PreviewSQL: got %v, want ErrForbidden", err)
	}
	if _, err := svc.Execute(ctx, 2, 100, draft.ID); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("LevelView Execute: got %v, want ErrForbidden", err)
	}
	if exec.calls != 0 {
		t.Fatalf("executor should not have been called when LevelView was rejected")
	}
}

func TestRunnerLinearVersioningAndFailureRecording(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	locker := &noopLocker{}
	exec := &stubExecutor{}
	checker := &stubChecker{
		grants: map[[2]int64]migration.AccessLevel{
			{10, 1}: migration.LevelEdit,
		},
	}
	fixedTime := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	runner := migration.NewRunner(repo, locker, exec, migration.WithClock(func() time.Time { return fixedTime }))
	svc := migration.NewService(repo, runner, checker)

	m1, err := svc.CreateAndExecute(ctx, 10, 1, "First migration", "CREATE TABLE t1 (id int);")
	if err != nil {
		t.Fatalf("first migration failed: %v", err)
	}
	if m1.Status != migration.StatusApplied || m1.Version == nil || *m1.Version != 1 {
		t.Fatalf("m1 = %+v, want status=applied version=1", m1)
	}
	if m1.ExecutedAt == nil || !m1.ExecutedAt.Equal(fixedTime) {
		t.Fatalf("m1.ExecutedAt = %v, want %v", m1.ExecutedAt, fixedTime)
	}

	exec.err = errors.New("ERROR: relation \"missing\" does not exist (SQLSTATE 42P01)")
	m2Draft, err := svc.CreateDraft(ctx, 10, 1, "Second failing migration", "INSERT INTO missing VALUES (1);")
	if err != nil {
		t.Fatalf("create second draft: %v", err)
	}
	m2, execErr := svc.Execute(ctx, 10, 1, m2Draft.ID)
	if execErr == nil {
		t.Fatal("expected execution error from failing migration")
	}
	if m2.Status != migration.StatusFailed || m2.Version == nil || *m2.Version != 2 {
		t.Fatalf("m2 = %+v, want status=failed version=2", m2)
	}
	if m2.ErrorMessage == nil || *m2.ErrorMessage == "" {
		t.Fatalf("expected error_message to be recorded on failed migration")
	}

	exec.err = nil
	m3, err := svc.CreateAndExecute(ctx, 10, 1, "Third migration", "CREATE TABLE t3 (id int);")
	if err != nil {
		t.Fatalf("third migration failed: %v", err)
	}
	if m3.Status != migration.StatusApplied || m3.Version == nil || *m3.Version != 3 {
		t.Fatalf("m3 = %+v, want status=applied version=3", m3)
	}

	callsBefore := exec.calls
	if _, err := svc.Execute(ctx, 10, 1, m1.ID); !errors.Is(err, migration.ErrAlreadyApplied) {
		t.Fatalf("re-executing applied migration: got %v, want ErrAlreadyApplied", err)
	}
	if exec.calls != callsBefore {
		t.Fatalf("executor should not be called on double-execute attempt")
	}

	if _, err := svc.Execute(ctx, 10, 1, m2.ID); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("re-executing failed migration: got %v, want ErrNotDraft", err)
	}
	if _, err := svc.UpdateDraft(ctx, 10, 1, m1.ID, "Edited", "SELECT 1;"); !errors.Is(err, migration.ErrAlreadyApplied) {
		t.Fatalf("editing applied migration: got %v, want ErrAlreadyApplied", err)
	}
	if _, err := svc.UpdateDraft(ctx, 10, 1, m2.ID, "Edited", "SELECT 1;"); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("editing failed migration: got %v, want ErrNotDraft", err)
	}
}

type externalLevel int

type externalCheckerStub struct{}

func (externalCheckerStub) EffectiveLevel(_ context.Context, userID, _ int64) (externalLevel, error) {
	if userID == 42 {
		return externalLevel(migration.LevelEdit), nil
	}
	return externalLevel(migration.LevelNone), nil
}

func TestAdaptChecker(t *testing.T) {
	adapted := migration.AdaptChecker[externalLevel](externalCheckerStub{})
	lvl, err := adapted.EffectiveLevel(context.Background(), 42, 1)
	if err != nil || lvl != migration.LevelEdit {
		t.Fatalf("AdaptChecker got (%v, %v), want (LevelEdit, nil)", lvl, err)
	}
}
