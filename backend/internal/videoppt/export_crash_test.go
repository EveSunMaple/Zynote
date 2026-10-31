package videoppt

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestKilledPPTExportReusesPublishedPDF(t *testing.T) {
	if directory := os.Getenv("ZYNOTE_TEST_PPT_CRASH_TASK"); directory != "" {
		body, err := os.ReadFile(filepath.Join(directory, "crash-input.json"))
		if err != nil {
			t.Fatal(err)
		}
		var input struct {
			Task    CreatedTask
			Choices []Choice
			Output  string
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Fatal(err)
		}
		if _, _, err := (Processor{}).Export(context.Background(), input.Task, input.Choices, input.Output); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "published.signal"), []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	processor, task, choices, output := pdfExportFixture(t)
	input, _ := json.Marshal(struct {
		Task    CreatedTask
		Choices []Choice
		Output  string
	}{task, choices, output})
	if err := os.WriteFile(filepath.Join(task.Dir, "crash-input.json"), input, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestKilledPPTExportReusesPublishedPDF$")
	command.Env = append(os.Environ(), "ZYNOTE_TEST_PPT_CRASH_TASK="+task.Dir)
	var log bytes.Buffer
	command.Stdout = &log
	command.Stderr = &log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(task.Dir, "published.signal")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("child never published: %s", log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	result, data, err := processor.Export(context.Background(), task, choices, output)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPDF(data, 1); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(output, "*.pdf"))
	if err != nil || len(files) != 1 || files[0] != result.Path {
		t.Fatalf("duplicate after killed publication: %v %v", files, err)
	}
}
