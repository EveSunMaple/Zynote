package videoppt

import (
	"context"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var playbackLeases = struct {
	sync.Mutex
	active map[string]bool
}{active: map[string]bool{}}

func (service *Service) withPlaybackLease(ctx context.Context, id string, run func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !operationIDPattern.MatchString(id) {
		return nil, contract.InputInvalid("任务编号无效")
	}
	if err := service.playbackRoot(); err != nil {
		return nil, err
	}
	if _, err := playbackPath(service.Root, true); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(service.Root)
	if err != nil {
		return nil, playbackUnavailable()
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	key := filepath.Join(root, id)
	playbackLeases.Lock()
	if playbackLeases.active[key] {
		playbackLeases.Unlock()
		return nil, contract.NewError(contract.CodeBusy, "该任务仍在执行，请先取消并等待结束")
	}
	playbackLeases.active[key] = true
	playbackLeases.Unlock()
	defer func() { playbackLeases.Lock(); delete(playbackLeases.active, key); playbackLeases.Unlock() }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return run(ctx)
}
func (service *Service) CheckPlaybackDispatch(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !operationIDPattern.MatchString(id) {
		return contract.InputInvalid("任务编号无效")
	}
	if err := service.playbackRoot(); err != nil {
		return err
	}
	if _, err := playbackPath(service.Root, true); err != nil {
		return err
	}
	if _, err := playbackPath(filepath.Join(service.Root, id), true); err != nil {
		return err
	}
	for _, name := range []string{"playback-release.json", ".playback-release-preparing.json"} {
		_, err := os.Lstat(filepath.Join(service.Root, id, name))
		if err == nil {
			return contract.NewError(contract.CodeConflict, "该视频已进入释放流程，不能重新执行")
		}
		if !os.IsNotExist(err) {
			return playbackUnavailable()
		}
	}

	return nil
}

// RunVideoTask shares the exact lifecycle lease with release prepare/finalize.
// A queued callback checks the durable tombstone only after acquiring this lease.
func (service *Service) RunVideoTask(ctx context.Context, id string, run func(context.Context) (any, error)) (any, error) {
	return service.withPlaybackLease(ctx, id, func(ctx context.Context) (any, error) {
		if err := service.CheckPlaybackDispatch(ctx, id); err != nil {
			return nil, err
		}
		return run(ctx)
	})
}
