package visual

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecognitionKilledProcessDoesNotReplay(t *testing.T) {
	if root := os.Getenv("ZYNOTE_TEST_VISUAL_CRASH_ROOT"); root != "" {
		service := New(root, "", "", "win32", false)
		_, _ = service.Recognize(context.Background(), os.Getenv("ZYNOTE_TEST_VISUAL_CRASH_ID"), "fixture", func(context.Context, Frame) (string, error) {
			if err := os.WriteFile(filepath.Join(root, "dispatched.signal"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			for {
				time.Sleep(time.Second)
			}
		}, nil)
		t.Fatal("child did not dispatch")
	}
	service, task := recognitionFixture(t)
	command := exec.Command(os.Args[0], "-test.run=^TestRecognitionKilledProcessDoesNotReplay$")
	command.Env = append(os.Environ(), "ZYNOTE_TEST_VISUAL_CRASH_ROOT="+service.Root, "ZYNOTE_TEST_VISUAL_CRASH_ID="+task.ID)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(service.Root, "dispatched.signal")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never reached dispatch")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	fresh := New(service.Root, service.FramesRoot, "", "win32", false)
	_, err := fresh.Recognize(context.Background(), task.ID, task.Connection, func(context.Context, Frame) (string, error) { t.Fatal("killed request replayed"); return "", nil }, nil)
	if err == nil {
		t.Fatal("unknown killed request accepted")
	}
}
