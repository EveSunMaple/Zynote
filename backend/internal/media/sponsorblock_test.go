package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func sponsorOptions(t *testing.T, categories string) NativeMediaOptions {
	t.Helper()
	options, err := MediaOptions(map[string]any{"sponsorblock": categories})
	if err != nil {
		t.Fatal(err)
	}
	return options
}

func TestSponsorBlockPlan(t *testing.T) {
	var calls int32
	fetcher := func(string, map[string]string, context.Context) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		body := `[{"videoID":"dQw4w9WgXcQ","segments":[
			{"segment":[10,20],"category":"sponsor","actionType":"skip","videoDuration":100,"UUID":"u1"},
			{"segment":[30,40],"category":"selfpromo","actionType":"skip","videoDuration":100,"UUID":"u2"},
			{"segment":[50,60],"category":"sponsor","actionType":"poi","videoDuration":100,"UUID":"u3"}
		]}]`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	directory := resolvedTemp(t)
	duration := 100.0
	source := Source{Platform: "youtube", SourceID: "dQw4w9WgXcQ", Catalog: Catalog{Duration: &duration}}
	plan, err := SponsorBlockPlan(source, sponsorOptions(t, "sponsor"), directory, fetcher, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removed) != 1 || plan.Removed[0].Start != 10 || plan.Removed[0].End != 20 ||
		plan.Duration != 90 || len(plan.Kept) != 2 || !strings.Contains(plan.Warning, "移除 1 段") ||
		!strings.Contains(plan.Warning, "10") {
		t.Fatalf("plan mismatch: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(directory, plan.Relative)); err != nil {
		t.Fatal(err)
	}
	if _, err := SponsorBlockPlan(source, sponsorOptions(t, "sponsor"), directory, fetcher, context.Background()); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("cached receipt should avoid network: %d", calls)
	}
	path := filepath.Join(directory, plan.Relative)
	body, _ := os.ReadFile(path)
	var receipt SponsorReceipt
	if err := json.Unmarshal(body, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Segments = append(receipt.Segments, SponsorSegment{Segment: [2]float64{70, 80}, Category: "sponsor", ActionType: "skip", VideoDuration: 100, UUID: "extra"})
	tampered, _ := json.MarshalIndent(receipt, "", "  ")
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SponsorBlockPlan(source, sponsorOptions(t, "sponsor"), directory, fetcher, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "恢复记录已改变") {
		t.Fatalf("tampered receipt mismatch: %v", err)
	}
}

func TestSponsorBlockEmptyAndValidation(t *testing.T) {
	notFound := func(string, map[string]string, context.Context) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: http.NoBody}, nil
	}
	duration := 100.0
	source := Source{Platform: "youtube", SourceID: "dQw4w9WgXcQ", Catalog: Catalog{Duration: &duration}}
	plan, err := SponsorBlockPlan(source, sponsorOptions(t, "sponsor"), resolvedTemp(t), notFound, context.Background())
	if err != nil || len(plan.Removed) != 0 || !strings.Contains(plan.Warning, "没有匹配") {
		t.Fatalf("empty plan mismatch: %+v %v", plan, err)
	}
	if _, err := SponsorBlockPlan(Source{Platform: "bilibili", SourceID: "dQw4w9WgXcQ", Catalog: Catalog{Duration: &duration}},
		sponsorOptions(t, "sponsor"), resolvedTemp(t), notFound, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "仅支持 YouTube") {
		t.Fatalf("platform mismatch: %v", err)
	}
	options := NativeMediaOptions{Sponsorblock: "unknown"}
	if _, err := SponsorBlockPlan(source, options, resolvedTemp(t), notFound, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "分类无效") {
		t.Fatalf("category mismatch: %v", err)
	}
	badDuration := func(string, map[string]string, context.Context) (*http.Response, error) {
		body := `[{"videoID":"dQw4w9WgXcQ","segments":[{"segment":[10,120],"category":"sponsor","actionType":"skip","videoDuration":100,"UUID":"u"}]}]`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	if _, err := SponsorBlockPlan(source, sponsorOptions(t, "sponsor"), resolvedTemp(t), badDuration, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "时长不符") {
		t.Fatalf("duration mismatch: %v", err)
	}
}
