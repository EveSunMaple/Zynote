package subtitlederived

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
)

func TestCrashWorker(t *testing.T) {
	if os.Getenv("ZYNOTE_CRASH_WORKER") != "1" {
		return
	}
	service := New(os.Getenv("ZYNOTE_CRASH_ROOT"), os.Getenv("ZYNOTE_CRASH_LIBRARY"))
	checkpoint := os.Getenv("ZYNOTE_CRASH_CHECKPOINT")
	block := func() {
		fmt.Println("CHECKPOINT")
		for {
			time.Sleep(time.Second)
		}
	}
	checks := 0
	id := os.Getenv("ZYNOTE_CRASH_ID")
	_, err := jobs.NewRegistry().RunQueued(context.Background(), filepath.Join(service.Root, "queue"), id, "notes", func(ctx context.Context) (any, error) {
		return service.Run(ctx, id, Dependencies{Generation: func(context.Context, string) (string, error) {
			checks++
			if checkpoint == "prepared" && checks == 2 {
				block()
			}
			return "g1", nil
		}, Summarize: func(context.Context, string, string) (string, []Point, error) {
			if checkpoint == "dispatched" {
				block()
			}
			return "# Generated", []Point{{Point: "Recovered result"}}, nil
		}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKilledProcessDoesNotReplayGeneration(t *testing.T) {
	for _, checkpoint := range []string{"dispatched", "prepared"} {
		t.Run(checkpoint, func(t *testing.T) { killedProcessCheckpoint(t, checkpoint) })
	}
}

func killedProcessCheckpoint(t *testing.T, checkpoint string) {
	service, task := recoveryTask(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestCrashWorker$")
	command.Env = append(os.Environ(), "ZYNOTE_CRASH_WORKER=1", "ZYNOTE_CRASH_CHECKPOINT="+checkpoint, "ZYNOTE_CRASH_ROOT="+service.Root, "ZYNOTE_CRASH_LIBRARY="+service.Library, "ZYNOTE_CRASH_ID="+task.ID)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	scanner := bufio.NewScanner(stdout)
	dispatched := false
	for scanner.Scan() {
		if scanner.Text() == "CHECKPOINT" {
			dispatched = true
			break
		}
	}
	if !dispatched {
		_ = command.Wait()
		t.Fatal("worker never reached requested checkpoint")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("worker should have been killed")
	}
	restarted := New(service.Root, service.Library)
	registry := jobs.NewRegistry()
	root := filepath.Join(service.Root, "queue")
	if record, err := registry.QueueRecord(root, task.ID); err != nil || record.State != "interrupted" {
		t.Fatalf("lost queue ownership after crash: %+v %v", record, err)
	}
	value, err := registry.RunQueued(context.Background(), root, task.ID, "notes", func(ctx context.Context) (any, error) {
		return restarted.Run(ctx, task.ID, Dependencies{Summarize: func(context.Context, string, string) (string, []Point, error) {
			t.Fatal("killed model request replayed")
			return "", nil, nil
		}})
	})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(Task)
	expectedStatus := "\u5931\u8d25"
	if checkpoint == "prepared" {
		expectedStatus = "\u5df2\u5b8c\u6210"
	}
	if result.Items[0].GenerationState != checkpoint || result.Items[0].Status != expectedStatus {
		t.Fatalf("unexpected recovery: %+v", result)
	}
}
