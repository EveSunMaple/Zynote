package media

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMediaOptionsDefaults(t *testing.T) {
	options, err := MediaOptions(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if options.Height != 0 || options.AudioQuality != "best" || options.Container != "mkv" ||
		options.FilenameTemplate != "%(title).100B [%(id)s].%(ext)s" || options.Downloader != "native" ||
		!options.SkipExisting || options.Privacy {
		t.Fatalf("defaults broken: %+v", options)
	}
	if !options.Metadata || !options.Chapters || !options.KeepSubtitles {
		t.Fatalf("boolean defaults broken: %+v", options)
	}
}

func TestMediaOptionsCrossRules(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]any
		message string
	}{
		{"cookie without use", map[string]any{"cookie_profile": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "选择登录档案时需启用使用登录档案"},
		{"clip end only", map[string]any{"clip_end": 10.0}, "片段结束时间必须大于开始时间"},
		{"bad sponsor", map[string]any{"sponsorblock": "wrong"}, "SponsorBlock 分类无效"},
		{"sponsor with subtitles", map[string]any{"sponsorblock": "sponsor", "subtitle_only": true}, "片段移除与仅字幕或章节拆分请分别建立任务"},
		{"bad proxy", map[string]any{"proxy": "ftp://proxy"}, "代理需为不含密码的 HTTP/SOCKS 地址"},
		{"bad template", map[string]any{"filename_template": "nested/%(title)s.%(ext)s"}, "文件名模板须为单层文件名，以 .%(ext)s 结尾"},
		{"template no ext", map[string]any{"filename_template": "video.mp4"}, "文件名模板须为单层文件名，以 .%(ext)s 结尾"},
		{"subtitle only embed", map[string]any{"subtitle_only": true, "embed_subtitles": true}, "仅字幕不能同时选纯音频、嵌入或裁剪"},
		{"embed without subs", map[string]any{"embed_subtitles": true}, "嵌入字幕需选择视频及下载字幕"},
		{"bad tuning", map[string]any{"custom_args": "--unknown 5"}, "此模板参数未接入或不是整数：--unknown"},
		{"tuning out of range", map[string]any{"custom_args": "--socket-timeout 999"}, "模板参数超出范围：--socket-timeout"},
		{"bad sort", map[string]any{"format_sort": "res:10"}, "数值排序不使用额外参数，请通过画质或音质上限设置"},
	}
	for _, testCase := range cases {
		if _, err := MediaOptions(testCase.options); err == nil || err.Error() != testCase.message {
			t.Fatalf("%s: expected %q, got %v", testCase.name, testCase.message, err)
		}
	}
	fine, err := MediaOptions(map[string]any{"clip_start": 1.0, "clip_end": 2.0, "sponsorblock": "sponsor,intro"})
	if err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	if fine.ClipEnd == nil || *fine.ClipEnd != 2.0 {
		t.Fatalf("clip passthrough broken: %+v", fine)
	}
	translated, _ := MediaOptions(map[string]any{"translated_subtitles": true})
	if !translated.AutoSubtitles || !translated.Subtitles {
		t.Fatalf("subtitle cascade broken: %+v", translated)
	}
	embedded, _ := MediaOptions(map[string]any{"embed_subtitles": true, "subtitles": true})
	if embedded.Container != "mkv" {
		t.Fatalf("embed forces mkv: %+v", embedded)
	}
	tuned, _ := MediaOptions(map[string]any{"custom_args": "--socket-timeout 30 --no-mtime"})
	if tuned == (NativeMediaOptions{}) {
		t.Fatal("tuning options should pass")
	}
}

func TestMediaSort(t *testing.T) {
	fields, err := MediaSort("+res,br,vcodec:h264")
	if err != nil || len(fields) != 3 {
		t.Fatalf("sort fields broken: %+v %v", fields, err)
	}
	if !fields[0].Ascending || fields[1].Ascending || fields[2].Preferred != "h264" {
		t.Fatalf("sort field values broken: %+v", fields)
	}
	rows := []NativeMediaStream{
		{ID: "b", Height: 720, Bandwidth: 1000, Codec: "avc1", Extension: "mp4"},
		{ID: "a", Height: 720, Bandwidth: 2000, Codec: "vp9", Extension: "webm"},
		{ID: "c", Height: 480, Bandwidth: 3000, Codec: "avc1", Extension: "mp4"},
	}
	sorted := SortMediaStreams(rows, "", false)
	if sorted[0].ID != "a" || sorted[1].ID != "b" || sorted[2].ID != "c" {
		t.Fatalf("default sort broken: %+v", sorted)
	}
	byHeight := SortMediaStreams(rows, "+res", false)
	if byHeight[0].ID != "c" {
		t.Fatalf("ascending height broken: %+v", byHeight)
	}
}

func writeJobFixture(t *testing.T, root string) (*DownloadJobs, string) {
	t.Helper()
	jobs := NewDownloadJobs(root, nil)
	if _, err := jobs.Create("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://example.com/video", "标题", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	list, err := jobs.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("create failed: %v %v", err, list)
	}
	return jobs, list[0].ID
}

func TestJobsLifecycle(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	root := t.TempDir()
	jobs, id := writeJobFixture(t, root)
	if _, err := os.Stat(filepath.Join(root, "native-downloads", id, "job.json")); err != nil {
		t.Fatalf("job record missing: %v", err)
	}
	created, _ := jobs.Read(id)
	if created.State != "queued" || created.Stage != "等待下载" || created.Revision != 0 {
		t.Fatalf("initial state broken: %+v", created)
	}
	if _, err := jobs.Transition(id, 0, "completed", "", "", "", nil); err == nil || err.Error() != "下载状态转换无效" {
		t.Fatalf("invalid transition allowed: %v", err)
	}
	if _, err := jobs.Transition(id, 99, "running", "", "", "", nil); err == nil || err.Error() != "下载记录已改变，请刷新后继续" {
		t.Fatalf("revision conflict not detected: %v", err)
	}
	running, err := jobs.Transition(id, 0, "running", "下载中", "", "", nil)
	if err != nil || running.State != "running" || running.Revision != 1 {
		t.Fatalf("transition broken: %+v %v", running, err)
	}

	artifact := filepath.Join(root, "native-downloads", id, "video.mp4")
	if err := os.WriteFile(artifact, []byte("artifact-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	receipt, err := jobs.Evidence(id, "video.mp4")
	if err != nil || receipt.Bytes != 14 {
		t.Fatalf("evidence broken: %+v %v", receipt, err)
	}
	if _, err := jobs.Evidence(id, "../escape.mp4"); err == nil || err.Error() != "成果路径不属于下载任务" {
		t.Fatalf("traversal not blocked: %v", err)
	}
	if _, err := jobs.Evidence(id, "job.json"); err == nil || err.Error() != "成果路径不属于下载任务" {
		t.Fatalf("reserved name not blocked: %v", err)
	}
	completed, err := jobs.Complete(id, 1, []Receipt{receipt}, []string{"video.part.mp4"})
	if err != nil || completed.State != "completed" || completed.Stage != "成果已核验" {
		t.Fatalf("complete broken: %+v %v", completed, err)
	}
	if _, err := jobs.Complete(id, 2, []Receipt{receipt}, nil); err == nil || !strings.Contains(err.Error(), "拒绝发布旧成果") {
		t.Fatalf("double complete allowed: %v", err)
	}
	path, err := jobs.File(id)
	pathExpected, _ := filepath.EvalSymlinks(artifact)
	if err != nil || (path != artifact && path != pathExpected) {
		t.Fatalf("file read broken: %v %v", path, err)
	}
	remaining, err := jobs.CleanupCompleted(id)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("cleanup broken: %v %v", remaining, err)
	}
	if _, err := os.Stat(filepath.Join(root, "native-downloads", id, "video.part.mp4")); !os.IsNotExist(err) {
		t.Fatalf("intermediate should be removed")
	}
}

func TestJobsRecovery(t *testing.T) {
	root := t.TempDir()
	jobs, id := writeJobFixture(t, root)
	running, err := jobs.Transition(id, 0, "running", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewDownloadJobs(root, nil)
	if err := restarted.Initialize(); err != nil {
		t.Fatal(err)
	}
	row, err := restarted.Read(id)
	if err != nil || row.State != "interrupted" || row.Stage != "上次中断，等待手动继续" || row.Revision != running.Revision+1 {
		t.Fatalf("recovery state broken: %+v %v", row, err)
	}
	if _, err := restarted.Transition(id, row.Revision, "queued", "", "", "", nil); err != nil {
		t.Fatalf("interrupted to queued blocked: %v", err)
	}
}

func TestJobsPrivacyEncrypted(t *testing.T) {
	root := t.TempDir()
	crypt := func(data []byte, decrypt bool) ([]byte, error) {
		if decrypt {
			if len(data) < 1 || data[0] != 0x1 {
				return nil, fmt.Errorf("密文无效")
			}
			return append([]byte{}, data[1:]...), nil
		}
		return append([]byte{0x1}, data...), nil
	}
	first := NewDownloadJobs(root, crypt)
	created, err := first.Create("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://example.com/v", "标题", map[string]any{"privacy": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "native-downloads", created.ID, "job.dpapi")); err != nil {
		t.Fatalf("encrypted record missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "native-downloads", created.ID, "job.json")); !os.IsNotExist(err) {
		t.Fatalf("plaintext record should not exist")
	}
	second := NewDownloadJobs(root, crypt)
	if err := second.Initialize(); err != nil {
		t.Fatalf("encrypted reload failed: %v", err)
	}
	reloaded, err := second.Read(created.ID)
	if err != nil || !reloaded.Recipe["privacy"].(bool) {
		t.Fatalf("reload broken: %+v %v", reloaded, err)
	}

	plainJobs := NewDownloadJobs(root, nil)
	if err := plainJobs.Initialize(); err == nil {
		t.Fatalf("plaintext reader on encrypted record should fail: %v", err)
	}
}

func TestJobsListOrder(t *testing.T) {
	root := t.TempDir()
	jobs := NewDownloadJobs(root, nil)
	first, err := jobs.Create("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://example.com/1", "一号", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := jobs.Create("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "https://example.com/2", "二号", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := jobs.List()
	if list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list order broken: %s %s", list[0].ID, list[1].ID)
	}
}

func TestLeaseAndLocks(t *testing.T) {
	root := t.TempDir()
	lease, err := AcquireDownloadQueue(root, "native-download-owner.sqlite")
	if err != nil {
		t.Fatalf("first lease failed: %v", err)
	}
	if _, err := AcquireDownloadQueue(root, "native-download-owner.sqlite"); err == nil || !strings.Contains(err.Error(), "已由另一个进程使用") {
		t.Fatalf("second lease should fail: %v", err)
	}
	lease.Close()
	lease2, err := AcquireDownloadQueue(root, "native-download-owner.sqlite")
	if err != nil {
		t.Fatalf("lease after close failed: %v", err)
	}
	lease2.Close()

	jobRoot := t.TempDir()
	directory := filepath.Join(jobRoot, "native-downloads", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	live := fmt.Sprintf(`{"version":1,"pid":%d,"identity":"%s:track-1"}`, os.Getpid(), identity)
	dead := fmt.Sprintf(`{"version":1,"pid":%d,"identity":"%s:track-2"}`, 999999999, identity)
	foreign := fmt.Sprintf(`{"version":1,"pid":%d,"identity":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:track-3"}`, 999999999)
	if err := os.WriteFile(filepath.Join(directory, "track-1.lock"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "track-2.lock"), []byte(dead), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "track-3.lock"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "track-4.lock"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovery, err := RecoverDownloadLocks(directory, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.Removed) != 1 || recovery.Removed[0] != "track-2.lock" {
		t.Fatalf("removal broken: %+v", recovery)
	}
	if len(recovery.Preserved) != 3 {
		t.Fatalf("preservation broken: %+v", recovery)
	}
	if _, err := os.Stat(filepath.Join(directory, "track-2.lock")); !os.IsNotExist(err) {
		t.Fatalf("dead lock not removed")
	}
	var stored map[string]any
	raw, _ := os.ReadFile(filepath.Join(directory, "track-1.lock"))
	if err := json.Unmarshal(raw, &stored); err != nil || stored["identity"] != identity+":track-1" {
		t.Fatalf("live lock content changed: %s", string(raw))
	}
}
