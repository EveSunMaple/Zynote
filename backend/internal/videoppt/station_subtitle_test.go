package videoppt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStationCheckpointRejectsChangedIdentityContentAndCounts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	task, err := service.CreateOperationTask("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "station", VideoOperation{Version: 1, Stage: "subtitle-station", URL: "https://www.bilibili.com/video/BV1234567890/?p=2", Page: 2, OutputName: "station.srt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"prepared", "no-subtitle"} {
		record := StationSubtitle{State: state}
		if state == "prepared" {
			record.SRT = "1\n00:00:00,000 --> 00:00:01,000\nfixture\n"
			record.Count = 1
		}
		if err := service.SaveStationSubtitle(task, record); err != nil {
			t.Fatal(err)
		}
		valid, found, err := service.LoadStationSubtitle(task)
		if err != nil || !found {
			t.Fatal(valid, found, err)
		}
		changes := []StationSubtitle{valid, valid, valid, valid}
		changes[0].Page = 1
		changes[1].URL = "https://www.bilibili.com/video/BV0987654321/?p=2"
		changes[2].SRT += "changed"
		changes[3].Count++
		for _, changed := range changes {
			if err := saveTaskJSON(filepath.Join(task.Dir, "station-subtitle.json"), changed); err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.LoadStationSubtitle(task); err == nil {
				t.Fatal("changed checkpoint accepted", changed)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "video-ready.json")); !os.IsNotExist(err) {
		t.Fatal("invented video proof", err)
	}
}
