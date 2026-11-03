package subtitlederived

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
)

func TestTwoServicesCannotTakeOverActiveRun(t *testing.T) {
	service, task := recoveryTask(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		result, err := service.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes, Summarize: func(context.Context, string, string) (string, []Point, error) {
			close(entered)
			<-release
			return "# Ready", []Point{{Cue: 0, Quote: "第一句", Point: "结论"}}, nil
		}})
		if err == nil && result.Items[0].Status != "已完成" {
			err = fmt.Errorf("first run not complete")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not enter provider")
	}
	other := New(service.Root, service.Library)
	_, err := other.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes})
	close(release)
	if err == nil {
		t.Fatal("active run was taken over")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestGenerationCASBeforeManifestCrashIsRecoverable(t *testing.T) {
	service, task := recoveryTask(t)
	ledger, err := generationjob.Open(filepath.Join(service.Root, "job-state"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := ledger.Acquire(context.Background(), task.ID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Close() // Crash: no task.ExecutionGeneration JSON was saved.
	restarted := New(service.Root, service.Library)
	result, err := restarted.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes, ValidateModel: func(context.Context) error { return fmt.Errorf("fixture local configuration") }})
	if err != nil || result.ExecutionGeneration != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	task.ExecutionGeneration, task.CacheEpoch = old.Generation, old.Epoch
	if err := service.save(task); !errors.Is(err, generationjob.ErrFence) {
		t.Fatalf("late old save: %v", err)
	}
	disk, err := service.Read(task.ID)
	if err != nil || disk.ExecutionGeneration != 2 {
		t.Fatalf("disk=%+v err=%v", disk, err)
	}
}

func TestStaleGenerationCannotPublishLocalBytes(t *testing.T) {
	service, task := recoveryTask(t)
	ctx := context.Background()
	ledger, err := generationjob.Open(filepath.Join(service.Root, "job-state"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	old, err := ledger.Acquire(ctx, task.ID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Acquire(ctx, task.ID, old.Generation, 256); err != nil {
		t.Fatal(err)
	}
	task.ExecutionGeneration, task.CacheEpoch = old.Generation, old.Epoch
	called := false
	err = service.withTaskFence(ctx, task, func() error { called = true; return nil })
	if !errors.Is(err, generationjob.ErrFence) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
