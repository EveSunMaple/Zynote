package orderedwork

import (
	"context"
	"sync"
)

// Run limits active calls to two and admitted, uncommitted work to four.
// Results commit in source order; a slow front bounds pending work.
//
// A failed call stops new admissions but intentionally does not cancel calls
// that already started. Those calls may already have been dispatched to a paid
// provider; letting them settle lets their validated results enter the success
// cache instead of being recorded as financially UNKNOWN. Parent cancellation
// still stops everything.
func Run[T any](
	ctx context.Context,
	count int,
	run func(context.Context, int) (T, error),
	commit func(T, int) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	type completion struct {
		index int
		value T
		err   error
	}
	completed := make(chan completion, 2)
	var workers sync.WaitGroup
	defer workers.Wait()
	var failureMutex sync.Mutex
	var firstFailure error
	recordFailure := func(err error) {
		failureMutex.Lock()
		defer failureMutex.Unlock()
		if firstFailure == nil {
			firstFailure = err
		}
	}
	failure := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		failureMutex.Lock()
		defer failureMutex.Unlock()
		return firstFailure
	}
	next, admitted, active := 0, 0, 0
	pending := make(map[int]T)
	stopped := false
	for {
		if !stopped {
			for active < 2 && admitted < count && admitted-next < 4 {
				if ctx.Err() != nil {
					stopped = true
					break
				}
				index := admitted
				admitted++
				active++
				workers.Add(1)
				go func() {
					defer workers.Done()
					value, err := run(ctx, index)
					completed <- completion{index: index, value: value, err: err}
				}()
			}
		}
		if active == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if firstFailure != nil {
				return firstFailure
			}
			if next == count {
				return nil
			}
			return failure()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-completed:
			active--
			if result.err != nil {
				recordFailure(result.err)
				stopped = true
				continue
			}
			if stopped {
				continue
			}
			pending[result.index] = result.value
			for {
				value, ok := pending[next]
				if !ok {
					break
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := commit(value, next); err != nil {
					recordFailure(err)
					stopped = true
					break
				}
				delete(pending, next)
				next++
			}
		}
	}
}
