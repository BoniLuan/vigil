package monitor

import (
	"context"
	"errors"
	"testing"

	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
)

func TestMonitorPersistenceLifecycle(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	service := NewService(NewStore(pool))
	ctx := context.Background()

	created, err := service.Create(ctx, CreateInput{Name: "FinPulse API", URL: "https://example.com/health"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID.Version() != uuid.Version(7) {
		t.Errorf("created UUID version = %d, want 7", created.ID.Version())
	}
	if created.State != StatePending || !created.Enabled || created.Version != 1 {
		t.Fatalf("unexpected created state: %+v", created)
	}
	var stateCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM monitor_states WHERE monitor_id = $1", created.ID).Scan(&stateCount); err != nil || stateCount != 1 {
		t.Fatalf("state relationship count = %d, error = %v", stateCount, err)
	}

	if _, err := service.Create(ctx, CreateInput{Name: "Duplicate", Slug: "finpulse-api", URL: "https://example.net"}); !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrSlugConflict", err)
	}

	name := "FinPulse Production API"
	public := true
	updated, err := service.Update(ctx, created.ID, PatchInput{Name: &name, Public: &public})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != name || !updated.Public || updated.URL != created.URL || updated.Version != 2 {
		t.Fatalf("unexpected updated monitor: %+v", updated)
	}

	paused, err := service.Pause(ctx, created.ID)
	if err != nil || paused.State != StatePaused || paused.Enabled {
		t.Fatalf("Pause() = %+v, %v", paused, err)
	}
	resumed, err := service.Resume(ctx, created.ID)
	if err != nil || resumed.State != StatePending || !resumed.Enabled {
		t.Fatalf("Resume() = %+v, %v", resumed, err)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM monitor_states WHERE monitor_id = $1", created.ID).Scan(&stateCount); err != nil || stateCount != 0 {
		t.Fatalf("state rows after delete = %d, error = %v", stateCount, err)
	}
	if err := service.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
}

func TestUpdateDetectsConcurrentWrite(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	service := NewService(NewStore(pool))
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{Name: "Concurrent", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	stale := created
	created.Name = "First update"
	if _, err := service.store.Update(ctx, created); err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
	stale.Name = "Stale update"
	if _, err := service.store.Update(ctx, stale); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("stale Update() error = %v, want ErrWriteConflict", err)
	}
}

func TestDisabledMonitorStartsPaused(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	service := NewService(NewStore(pool))
	enabled := false
	created, err := service.Create(context.Background(), CreateInput{Name: "Paused", URL: "https://example.com", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if created.Enabled || created.State != StatePaused {
		t.Fatalf("created monitor = %+v", created)
	}
}
