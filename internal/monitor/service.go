package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	store *Store
}

func NewService(store *Store) *Service { return &Service{store: store} }

func (s *Service) Create(ctx context.Context, input CreateInput) (Monitor, error) {
	value, err := prepareCreate(input)
	if err != nil {
		return Monitor{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Monitor{}, fmt.Errorf("generate monitor id: %w", err)
	}
	value.ID = id
	if value.Enabled {
		value.State = StatePending
	} else {
		value.State = StatePaused
	}
	return s.store.Create(ctx, value)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Monitor, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, options ListOptions) ([]Monitor, error) {
	if options.Limit <= 0 {
		options.Limit = 50
	}
	if options.Limit > 100 {
		options.Limit = 100
	}
	if options.Offset < 0 {
		options.Offset = 0
	}
	return s.store.List(ctx, options)
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, patch PatchInput) (Monitor, error) {
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return Monitor{}, err
	}
	if current.ArchivedAt != nil {
		return Monitor{}, ErrArchived
	}
	applyPatch(&current, patch)
	if err := Validate(current); err != nil {
		return Monitor{}, err
	}
	return s.store.Update(ctx, current)
}

func (s *Service) Pause(ctx context.Context, id uuid.UUID) (Monitor, error) {
	return s.store.setOperationalState(ctx, id, false, StatePaused)
}

func (s *Service) Resume(ctx context.Context, id uuid.UUID) (Monitor, error) {
	return s.store.setOperationalState(ctx, id, true, StatePending)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.Archive(ctx, id)
}

func applyPatch(value *Monitor, patch PatchInput) {
	if patch.Name != nil {
		value.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Slug != nil {
		value.Slug = NormalizeSlug(*patch.Slug)
	}
	if patch.Description.Set {
		value.Description = normalizeDescription(patch.Description.Value)
	}
	if patch.URL != nil {
		value.URL = strings.TrimSpace(*patch.URL)
	}
	if patch.HTTPMethod != nil {
		value.HTTPMethod = strings.ToUpper(strings.TrimSpace(*patch.HTTPMethod))
	}
	if patch.ExpectedStatusMin != nil {
		value.ExpectedStatusMin = *patch.ExpectedStatusMin
	}
	if patch.ExpectedStatusMax != nil {
		value.ExpectedStatusMax = *patch.ExpectedStatusMax
	}
	if patch.Interval != nil {
		value.Interval = *patch.Interval
	}
	if patch.Timeout != nil {
		value.Timeout = *patch.Timeout
	}
	if patch.FailureThreshold != nil {
		value.FailureThreshold = *patch.FailureThreshold
	}
	if patch.RecoveryThreshold != nil {
		value.RecoveryThreshold = *patch.RecoveryThreshold
	}
	if patch.Public != nil {
		value.Public = *patch.Public
	}
}
