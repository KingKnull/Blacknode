package service

import (
	"context"
	"sync"
	"time"
)

// InfrastructureService provides persisted infrastructure tools to both the
// desktop and the standalone runner. External tools run only on explicit use.
type InfrastructureService struct {
	platform  *PlatformService
	jobs      *JobService
	books     *RunbookService
	notify    *NotificationService
	mu        sync.Mutex
	stop      context.CancelFunc
	wg        sync.WaitGroup
	syncPlans map[string]syncReview
}

func NewInfrastructureService(p *PlatformService, jobs *JobService, books *RunbookService, notify *NotificationService) *InfrastructureService {
	return &InfrastructureService{platform: p, jobs: jobs, books: books, notify: notify, syncPlans: map[string]syncReview{}}
}

func (s *InfrastructureService) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.platform.expireChanges(false)
				s.pollMonitors(ctx)
			}
		}
	}()
}

func (s *InfrastructureService) Stop() {
	s.mu.Lock()
	cancel := s.stop
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.platform.expireChanges(true)
	s.mu.Lock()
	s.stop = nil
	s.mu.Unlock()
}
