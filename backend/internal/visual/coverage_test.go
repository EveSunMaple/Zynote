package visual

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coveredTask(t *testing.T) (*Service, Task) {
	t.Helper()
	service, _ := controlledMediaService(t, "producer")
	source := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(source, []byte("controlled video"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err := service.Create(context.Background(), source, 4, "balanced", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range task.Frames {
		if frame.Extraction == nil {
			t.Fatal("actual extraction producer omitted receipt")
		}
	}
	return service, task
}

func TestCoverageSurvivesCleanupAndListIsReadOnly(t *testing.T) {
	service, task := coveredTask(t)
	result, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) { return "Recognized text", nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cleaned || result.Coverage.Extracted.Count != 4 || result.Coverage.Recognized.Count != 4 || result.Coverage.Submitted.State != "unknown" || result.Coverage.Cited.State != "unknown" {
		t.Fatalf("wrong retained coverage %+v", result.Coverage)
	}
	for _, frame := range result.Frames {
		if _, err := os.Stat(frame.Path); !os.IsNotExist(err) {
			t.Fatal("fixture did not clean frames")
		}
	}
	body, err := os.ReadFile(result.Output)
	if err != nil || !strings.Contains(string(body), "已提取：4/4") || !strings.Contains(string(body), "已识别并保存文字：4/4") || !strings.Contains(string(body), "已被摘要引用：未记录") {
		t.Fatalf("export coverage missing %s %v", body, err)
	}
	path := filepath.Join(service.Root, task.ID+".json")
	before, _ := os.ReadFile(path)
	if strings.Contains(string(before), `"coverage"`) {
		t.Fatal("derived counters persisted")
	}
	fresh := New(service.Root, service.FramesRoot, service.Resources, service.Platform, false)
	rows, err := fresh.List()
	after, _ := os.ReadFile(path)
	if err != nil || len(rows) != 1 || rows[0].Coverage.Recognized.State != "recorded" || string(before) != string(after) {
		t.Fatalf("list mutated or lost cleanup receipt %+v %v", rows, err)
	}
}

func TestCoverageCountsOnlySavedNonemptyResponses(t *testing.T) {
	for _, mode := range []string{"empty", "error", "cancel", "partial", "frame-change"} {
		t.Run(mode, func(t *testing.T) {
			service, task := coveredTask(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			_, _ = service.Recognize(ctx, task.ID, "fixture", func(_ context.Context, frame Frame) (string, error) {
				calls++
				switch mode {
				case "empty":
					return " ", nil
				case "error":
					return "", errors.New("controlled transport error")
				case "cancel":
					cancel()
					return "response after cancellation", nil
				case "partial":
					if calls == 1 {
						return "saved response", nil
					}
					return "", errors.New("controlled transport error")
				case "frame-change":
					if err := os.WriteFile(frame.Path, []byte("modified"), 0600); err != nil {
						t.Fatal(err)
					}
					return "response", nil
				}
				return "", nil
			}, nil)
			rows, err := service.List()
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			coverage := rows[0].Coverage
			expected := 0
			if mode == "partial" {
				expected = 1
			}
			if coverage.Recognized.Count != expected || coverage.Submitted.State != "unknown" || coverage.Cited.State != "unknown" {
				t.Fatalf("unreceived response counted %+v", coverage)
			}
			if mode == "frame-change" {
				if coverage.Extracted.State != "invalid" {
					t.Fatal("changed frame retained valid receipt")
				}
				if _, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) {
					t.Fatal("changed frame dispatched new request")
					return "", nil
				}, nil); err == nil {
					t.Fatal("changed frame accepted")
				}
			}
			if mode == "partial" {
				saved, err := service.RecoverOutput(task.ID)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := os.ReadFile(saved.Output)
				if !strings.Contains(string(body), "已识别并保存文字：1/4") {
					t.Fatalf("recover export failed to project partial facts: %s", body)
				}
			}
		})
	}
}

func TestRetainedCoverageRejectsSourceFrameResponseConnectionAndOutputChanges(t *testing.T) {
	for _, mode := range []string{"source", "frame-hash", "response", "connection", "output"} {
		t.Run(mode, func(t *testing.T) {
			service, task := coveredTask(t)
			result, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) { return "Recognized text", nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source":
				if err := os.WriteFile(result.Source, []byte("changed source"), 0600); err != nil {
					t.Fatal(err)
				}
			case "frame-hash":
				result.Frames[0].Hash = strings.Repeat("a", 64)
			case "response":
				result.Frames[0].Text = "changed response"
			case "connection":
				result.Connection = "changed connection"
			case "output":
				if err := os.WriteFile(result.Output, []byte("changed output"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.save(result); err != nil {
				t.Fatal(err)
			}
			rows, err := service.List()
			if err != nil || len(rows) != 1 || rows[0].Coverage.Extracted.State != "invalid" || rows[0].Coverage.Recognized.State != "invalid" {
				t.Fatalf("changed receipt accepted %+v %v", rows, err)
			}
			if _, err := service.Recognize(context.Background(), task.ID, result.Connection, func(context.Context, Frame) (string, error) {
				t.Fatal("invalid retained receipt dispatched request")
				return "", nil
			}, nil); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}

func TestLegacyCleanedTaskKeepsUnknownCoverageAndCompatibility(t *testing.T) {
	service, task := recognitionFixture(t)
	task.Cleaned = true
	task.Frames[0].Status = StatusDone
	task.Frames[0].Text = "legacy text"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	if err := service.validateFrameReceipts(&task); err != nil {
		t.Fatal("new evidence layer rejected legacy cleaned task", err)
	}
	coverage := service.projectCoverage(&task)
	if coverage.Extracted.State != "unknown" || coverage.Recognized.State != "unknown" {
		t.Fatal("legacy statuses fabricated receipts")
	}
	path := filepath.Join(service.Root, task.ID+".json")
	encoded, _ := os.ReadFile(path)
	var stored map[string]any
	if json.Unmarshal(encoded, &stored) != nil {
		t.Fatal("fixture malformed")
	}
	stored["coverage"] = map[string]any{"extracted": map[string]any{"state": "recorded", "recordedCount": 999, "total": 999}}
	encoded, _ = json.Marshal(stored)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := service.List()
	after, _ := os.ReadFile(path)
	if err != nil || len(rows) != 1 || rows[0].Coverage.Extracted.State != "unknown" || string(after) != string(encoded) {
		t.Fatal("serialized fake counters trusted or read-only list wrote task")
	}
	recovered, err := service.RecoverOutput(task.ID)
	if err != nil {
		t.Fatal("legacy output recovery changed", err)
	}
	body, _ := os.ReadFile(recovered.Output)
	if !strings.Contains(string(body), "已提取：未记录") {
		t.Fatal("legacy export fabricated coverage")
	}
}

func TestCleanedChangedOutputRecoveryRetainsInvalidCoverageConsistently(t *testing.T) {
	service, task := coveredTask(t)
	calls := 0
	original, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) { calls++; return "Saved text", nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := "User-modified original output"
	if err := os.WriteFile(original.Output, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.RecoverOutput(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Output == original.Output || recovered.Coverage.Extracted.State != "invalid" || calls != 4 {
		t.Fatalf("unsafe recovery %+v", recovered)
	}
	previous, _ := os.ReadFile(original.Output)
	body, _ := os.ReadFile(recovered.Output)
	if !strings.Contains(string(body), "恢复文件已生成，历史覆盖记录需重新核对") {
		t.Fatal("recovery success and unverified history were not distinguished")
	}
	if string(previous) != changed || !strings.Contains(string(body), "已提取：记录失效") || !strings.Contains(string(body), "已识别并保存文字：记录失效") {
		t.Fatalf("recovery overwrote original or falsely verified: %s", body)
	}
	rows, err := New(service.Root, service.FramesRoot, service.Resources, service.Platform, false).List()
	if err != nil || len(rows) != 1 || rows[0].Coverage.Extracted.State != "invalid" || rows[0].Coverage.Recognized.State != "invalid" {
		t.Fatalf("export/list disagreed %+v %v", rows, err)
	}
	if _, err := service.Recognize(context.Background(), task.ID, "fixture", func(context.Context, Frame) (string, error) {
		t.Fatal("invalid recovery replayed model")
		return "", nil
	}, nil); err == nil {
		t.Fatal("invalid recovery automatically regained coverage")
	}
}
