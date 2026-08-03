package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/run"
)

const (
	maxPlanBytes      int64 = 16 << 20
	maxAgentJSONBytes int64 = 8 << 20
	maxContextBytes   int64 = 16 << 20
)

type Service struct {
	store     *run.Store
	collector *gitcontext.Collector
	now       func() time.Time
}

func New(runRoot string) (*Service, error) {
	var (
		store *run.Store
		err   error
	)
	if runRoot == "" {
		store, err = run.NewDefaultStore()
	} else {
		store, err = run.NewStore(runRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("create run store: %w", err)
	}
	collector, err := gitcontext.NewCollector(gitcontext.NewSystemRunner(30 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("create Git collector: %w", err)
	}
	return &Service{store: store, collector: collector, now: time.Now}, nil
}

func (service *Service) StoreRoot() string { return service.store.Root() }

func requireService(service *Service) error {
	if service == nil || service.store == nil || service.collector == nil {
		return errors.New("workflow service is not initialized")
	}
	return nil
}

func (service *Service) lockRun(ctx context.Context, runID string) (func(), error) {
	unlock, err := service.store.Lock(ctx, runID)
	if err != nil {
		return nil, err
	}
	return func() { _ = unlock() }, nil
}
