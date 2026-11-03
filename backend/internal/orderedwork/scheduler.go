package orderedwork

import (
	"context"
	"sync"
)

// Run limits active calls to two and admitted, uncommitted work to four.
// Results commit in source order; a slow front bounds pending work.
func Run[T any](
	ctx context.Context,
	count int,
	run func(context.Context, int) (T, error),
	commit func(T, int) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workerContext, cancel := context.WithCancel(ctx)
	type completion struct {
		index int
		value T
		err   error
	}
	completed := make(chan completion, 2)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	var failureMutex sync.Mutex
	var firstFailure error
	failure := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		failureMutex.Lock()
		defer failureMutex.Unlock()
		if firstFailure != nil {
			return firstFailure
		}
		return workerContext.Err()
	}
	next, admitted, active := 0, 0, 0
	pending := make(map[int]T)
	for next < count {
		for active < 2 && admitted < count && admitted-next < 4 {
			if workerContext.Err() != nil {
				return failure()
			}
			index := admitted
			admitted++
			active++
			workers.Add(1)
			go func() {
				defer workers.Done()
				value, err := run(workerContext, index)
				if err != nil {
					failureMutex.Lock()
					if firstFailure == nil {
						firstFailure = err
					}
					failureMutex.Unlock()
					cancel()
				}
				completed <- completion{index: index, value: value, err: err}
			}()
		}
		select {
		case <-workerContext.Done():
			return failure()
		case result := <-completed:
			active--
			if result.err != nil {
				return failure()
			}
			pending[result.index] = result.value
			for {
				value, ok := pending[next]
				if !ok {
					break
				}
				if workerContext.Err() != nil {
					return failure()
				}
				if err := commit(value, next); err != nil {
					cancel()
					return err
				}
				delete(pending, next)
				next++
			}
		}
	}
	return nil
}
