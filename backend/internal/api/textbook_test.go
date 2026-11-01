package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/textbook"
)

func TestTextbookDirectoryWalksUp(t *testing.T) {
	root := t.TempDir()
	textbook := filepath.Join(root, "课本")
	if err := os.MkdirAll(textbook, 0o755); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "Contents", "MacOS")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := textbookDirectory(start); got != textbook {
		t.Fatalf("unexpected directory: %q", got)
	}
}

func TestTextbookDirectoryMissing(t *testing.T) {
	if got := textbookDirectory(t.TempDir()); got != "" {
		t.Fatalf("unexpected directory: %q", got)
	}
}

func TestAnalyzeTextbookPagesBoundsConcurrencyAndKeepsOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan int, 3)
	gates := map[int]chan struct{}{4: make(chan struct{}), 5: make(chan struct{}), 6: make(chan struct{})}
	var progress []int
	var rows []textbook.Highlight
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		rows, err = analyzeTextbookPages(ctx, 4, 6, func(ctx context.Context, page int) ([]textbook.Highlight, error) {
			started <- page
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-gates[page]:
				return []textbook.Highlight{{Page: page}}, nil
			}
		}, func(n int) { progress = append(progress, n) })
	}()
	waitStart := func() int {
		t.Helper()
		select {
		case p := <-started:
			return p
		case <-ctx.Done():
			t.Fatal("selected pages did not start in parallel")
			return 0
		}
	}
	pair := []int{waitStart(), waitStart()}
	slices.Sort(pair)
	if !slices.Equal(pair, []int{4, 5}) {
		t.Fatalf("first two pages: %v", pair)
	}
	close(gates[5])
	if waitStart() != 6 {
		t.Fatal("expected page 6")
	}
	close(gates[6])
	close(gates[4])
	<-done
	if err != nil || !slices.Equal(progress, []int{1, 2, 3}) || len(rows) != 3 || rows[0].Page != 4 || rows[1].Page != 5 || rows[2].Page != 6 {
		t.Fatalf("completion: rows=%v progress=%v error=%v", rows, progress, err)
	}
}

func TestAnalyzeTextbookPagesCancelDiscardsPartialResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := 0
	rows, err := analyzeTextbookPages(ctx, 1, 8, func(ctx context.Context, page int) ([]textbook.Highlight, error) {
		if page == 1 {
			return []textbook.Highlight{{Page: 1}}, nil
		}
		if page > 2 {
			t.Error("started another page after cancellation")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}, func(n int) {
		completed = n
		cancel()
	})
	if !errors.Is(err, context.Canceled) || rows != nil || completed != 1 {
		t.Fatalf("canceled result: %v %v", rows, err)
	}
}

func TestTextbookVisionCoordinateFormats(t *testing.T) {
	for _, input := range []string{"[100,250,600,500]", "[0.1,0.25,0.6,0.5]"} {
		got, ok := normalizedTextbookRect([]byte(input))
		if !ok || got != [4]float64{0.1, 0.25, 0.6, 0.5} {
			t.Fatalf("rectangle %s: %v", input, got)
		}
	}
	for _, input := range []string{"[1,2,3]", "[1,2,3,4,5]", "[1,2,1001,4]", "[20,10,10,90]", "[0.1,100,600,700]", "[-5,2,3,4]", "[null,20,200,250]"} {
		if _, ok := normalizedTextbookRect([]byte(input)); ok {
			t.Fatalf("accepted invalid rectangle %s", input)
		}
	}
}

func TestReviewScannedSuggestionsKeepsOnlyVerifiedRows(t *testing.T) {
	const speech = "上课时水的沸点是一百摄氏度。"
	sources := textbookSources{Teacher: speech, Replay: []textbookEvidence{{Source: "teacher", Text: speech, Timestamp: "00:00:07"}}}
	catalog := makeTextbookEvidence(sources)
	proposals := `[
	 {"rect":[10,20,100,40],"evidenceId":1,"quote":"沸点"},
	 {"rect":[10,20,100,40],"evidenceId":1,"evidence":"编造的课堂依据","quote":"假依据"},
	 {"rect":[10,20,100,40],"evidenceId":1,"source":"focus","quote":"错来源"},
	 {"rect":[10,20,100,40],"evidenceId":9,"quote":"假编号"},
	 {"rect":[10,20,5,30],"evidenceId":1,"quote":"错位置"},
	 {"rect":[10,20,100,40],"evidenceId":1}
	]`
	rows, warning, err := reviewScannedSuggestions([]byte(proposals), 3, sampleScanPlacement(), sources, catalog)
	if err != nil || len(rows) != 1 || rows[0].Quote != "沸点" || rows[0].Evidence != sources.Teacher || rows[0].Source != "teacher" {
		t.Fatalf("verified rows: %+v, %v", rows, err)
	}
	if warning != (textbookScanWarning{Page: 3, Evidence: 3, Coordinates: 1, Invalid: 1}) {
		t.Fatalf("wrong reasons: %+v", warning)
	}
}
