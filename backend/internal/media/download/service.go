package download

import (
	"context"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

type Service struct {
	Runner   *Runner
	Jobs     *media.DownloadJobs
	Finished func(id string)
	mu       sync.Mutex
	active   map[string]context.CancelFunc
	queue    chan string
}

func NewService(runner *Runner) *Service {
	service := &Service{
		Runner: runner,
		Jobs:   runner.Jobs,
		active: map[string]context.CancelFunc{},
		queue:  make(chan string, 1024),
	}
	go service.pump()
	return service
}

func (service *Service) pump() {
	for id := range service.queue {
		job, err := service.Jobs.Read(id)
		if err != nil || job.State != "queued" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		service.mu.Lock()
		service.active[id] = cancel
		service.mu.Unlock()
		_, _ = service.Runner.Run(id, ctx)
		if service.Finished != nil {
			service.Finished(id)
		}
		service.mu.Lock()
		delete(service.active, id)
		service.mu.Unlock()
		cancel()
	}
}

func (service *Service) Enqueue(id string) { service.queue <- id }

func (service *Service) Cancel(id string) bool {
	service.mu.Lock()
	cancel, ok := service.active[id]
	service.mu.Unlock()
	if ok {
		cancel()
		return true
	}
	return false
}

func (service *Service) Active(id string) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	_, ok := service.active[id]
	return ok
}
