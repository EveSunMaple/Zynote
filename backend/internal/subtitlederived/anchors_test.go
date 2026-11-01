package subtitlederived

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestAnchorRebuiltFromExactCue(t *testing.T) {
	srt := "1\n00:00:01,125 --> 00:00:03,750\nOriginal words\n"
	points, markdown, err := anchorPoints(srt, []Point{{Cue: 0, Quote: "Original", Timestamp: "99:00:00", Anchor: &CueAnchor{StartMs: 999999}}})
	if err != nil {
		t.Fatal(err)
	}
	anchor := points[0].Anchor
	if anchor.SourceHash != digest(srt) || anchor.CueIndex != 0 || anchor.StartMs != 1125 || anchor.EndMs != 3750 || anchor.Text != "Original words" || points[0].Timestamp != "00:00:01" || !strings.Contains(markdown, "1125–3750") {
		t.Fatalf("unbound anchor %+v %s", points, markdown)
	}
	for _, point := range []Point{{Cue: -1, Quote: "Original"}, {Cue: 0.5, Quote: "Original"}, {Cue: 1, Quote: "Original"}, {Cue: 0, Quote: "invented"}} {
		if _, _, err := anchorPoints(srt, []Point{point}); err == nil {
			t.Fatalf("invalid anchor accepted %+v", point)
		}
	}
}

func TestPublishedPreparedPairsRetainVersionedLayout(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			service, task := recoveryTask(t)
			task.OutputVersion = version // Existing pre-anchor manifest; no migration in place.
			prepared := &Prepared{Markdown: "# Original response", Points: []Point{{Quote: "第一句", Point: "原结论"}}}
			task.Items[0].Prepared = prepared
			if err := service.save(task); err != nil {
				t.Fatal(err)
			}
			first, err := service.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes})
			if err != nil || first.Items[0].Status != "已完成" {
				t.Fatalf("legacy export failed %+v %v", first, err)
			}
			originals := map[string]string{}
			for _, output := range first.Items[0].Outputs {
				body, _ := os.ReadFile(output.Path)
				originals[output.Path] = string(body)
				if version == 0 && (strings.Contains(string(body), "\"anchor\"") || strings.Contains(string(body), "字幕引用定位")) {
					t.Fatal("legacy layout changed")
				}
			}
			// Simulate old process death after publication but before Outputs receipt.
			task.Items[0].Outputs = nil
			task.Items[0].Prepared = prepared
			if err := service.save(task); err != nil {
				t.Fatal(err)
			}
			recovered, err := service.Run(context.Background(), task.ID, Dependencies{ResolveRecipes: fixtureRecipes, Summarize: func(context.Context, string, string) (string, []Point, error) {
				t.Fatal("legacy recovery replayed provider")
				return "", nil, nil
			}})
			if err != nil || recovered.Items[0].Status != "已完成" {
				t.Fatalf("legacy pair not recovered %+v %v", recovered, err)
			}
			for path, want := range originals {
				body, _ := os.ReadFile(path)
				if string(body) != want {
					t.Fatal("legacy output overwritten")
				}
			}
		})
	}
}
