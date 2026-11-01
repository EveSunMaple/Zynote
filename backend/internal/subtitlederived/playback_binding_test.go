package subtitlederived

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const boundDerivedID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
const boundVideoID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
const boundSRT = "1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"

func boundDerivedFixture(t *testing.T) (*Service, Task, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	video := videoppt.New(filepath.Join(root, "VideoTasks"))
	source := filepath.Join(root, "source.mp4")
	_ = os.WriteFile(source, []byte("media"), 0600)
	hash := digest("media")
	task, err := video.CreateOperationTask(boundVideoID, "fixture", videoppt.VideoOperation{Version: 1, Stage: "subtitle", Path: source, SourceHash: hash, OutputName: "fixture.srt", Page: 1, RetainPlayback: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := video.ReservePlaybackRetention(ctx, task.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := videoppt.CopyVideoReady(ctx, source, task.Path); err != nil {
		t.Fatal(err)
	}
	if err := videoppt.SavePreparedSubtitle(task, boundSRT, 1); err != nil {
		t.Fatal(err)
	}
	if err := video.PreparePlaybackRetention(ctx, task.ID, boundSRT); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Transcripts")
	_ = os.MkdirAll(dir, 0700)
	path, err := videoppt.PublishSubtitle(ctx, task, dir, "fixture.srt", []byte(boundSRT))
	if err != nil {
		t.Fatal(err)
	}
	if err := video.MarkSubtitleSaved(task.ID, path); err != nil {
		t.Fatal(err)
	}
	binding, err := video.CompletePlaybackRetention(ctx, task.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := video.RetainedSubtitle(ctx, task.ID, binding.RetentionReceiptSHA256, "")
	if err != nil {
		t.Fatal(err)
	}
	service := New(filepath.Join(root, "NoteGeneration"), root)
	derived, err := service.CreateRetainedOperation(ctx, verified, "brief", "", boundDerivedID)
	if err != nil {
		t.Fatal(err)
	}
	return service, derived, path, task.Path
}

func TestBoundDerivedRejectsJournalInjectionAndDowngrade(t *testing.T) {
	for _, mutation := range []string{"binding", "hash", "request", "source", "course", "downgrade", "authority"} {
		t.Run(mutation, func(t *testing.T) {
			service, task, _, _ := boundDerivedFixture(t)
			switch mutation {
			case "binding":
				task.Items[0].SourceBinding.VideoTaskID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			case "hash":
				task.Items[0].SourceBindingSHA256 = digest("other")
			case "request":
				task.RequestHash = digest("other")
			case "source":
				task.Items[0].Source.Path = filepath.Join(service.Root, "inputs", "other.srt")
			case "course":
				task.CourseName = "other"
			case "downgrade":
				task.Items[0].SourceBinding = nil
				task.Items[0].SourceBindingSHA256 = ""
				task.Items[0].BindingAuthoritySHA256 = ""
			case "authority":
				_ = os.WriteFile(filepath.Join(service.Root, task.ID+".playback-binding.json"), []byte("{}"), 0600)
			}
			if err := service.save(task); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Read(task.ID); err == nil {
				t.Fatal("tampered journal accepted")
			}
			if _, err := service.Run(context.Background(), task.ID, Dependencies{Summarize: func(context.Context, string, string) (string, []Point, error) {
				t.Fatal("provider reached")
				return "", nil, nil
			}}); err == nil {
				t.Fatal("tampered run accepted")
			}
		})
	}
}

func TestBoundDerivedOldIngressCannotAdoptOrInject(t *testing.T) {
	service, task, _, _ := boundDerivedFixture(t)
	if _, err := service.CreateDirectOperation(boundSRT, task.Items[0].Source.Title, "brief", "", task.ID); err == nil {
		t.Fatal("bound operation adopted through unbound creation")
	}
	if _, err := service.CreateOperation([]Source{task.Items[0].Source}, []string{"brief"}, "", task.ID); err == nil {
		t.Fatal("bound operation adopted through general creation")
	}
	legacy, err := service.CreateDirectOperation(boundSRT, "legacy", "brief", "", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	legacy.Items[0].SourceBinding = task.Items[0].SourceBinding
	legacy.Items[0].SourceBindingSHA256 = task.Items[0].SourceBindingSHA256
	if err := service.save(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(legacy.ID); err == nil {
		t.Fatal("same-hash legacy promoted")
	}
	if _, err := service.CreateRetainedOperation(context.Background(), videoppt.VerifiedRetainedSubtitle{}, "brief", "", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"); err == nil {
		t.Fatal("forged zero descriptor accepted")
	}
}

func TestBoundDerivedLiveCheckImmediatelyBeforeDispatchAndTextRecovery(t *testing.T) {
	service, task, saved, video := boundDerivedFixture(t)
	calls := 0
	deps := Dependencies{ResolveRecipes: fixtureRecipes, ValidateModel: func(context.Context) error { return os.WriteFile(saved, []byte("changed"), 0600) }, Summarize: func(context.Context, string, string) (string, []Point, error) {
		calls++
		return "# Summary", []Point{{Cue: 0, Quote: "Saved words", Point: "Key", Topic: "Topic"}}, nil
	}}
	run, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls != 0 || run.Items[0].GenerationState != "" {
		t.Fatal("failed live check dispatched", calls, run, err)
	}
	if err := os.WriteFile(saved, []byte(boundSRT), 0600); err != nil {
		t.Fatal(err)
	}
	deps.ValidateModel = nil
	deps.Summarize = func(context.Context, string, string) (string, []Point, error) {
		calls++
		_ = os.Remove(video)
		return "# Summary", []Point{{Cue: 0, Quote: "Saved words", Point: "Key", Topic: "Topic"}}, nil
	}
	run, err = service.Run(context.Background(), task.ID, deps)
	if err != nil || calls != 1 || run.Items[0].Status != "已完成" {
		body, _ := json.Marshal(run)
		t.Fatal("observed result lost after media loss", calls, string(body), err)
	}
	deps.Summarize = func(context.Context, string, string) (string, []Point, error) {
		t.Fatal("completed result replayed")
		return "", nil, nil
	}
	if _, err := service.Run(context.Background(), task.ID, deps); err != nil {
		t.Fatal("text recovery blocked by media", err)
	}
	if _, err := videoppt.New(filepath.Join(service.Library, "VideoTasks")).ValidateRetainedPlaybackBinding(context.Background(), *task.Items[0].SourceBinding, ""); err == nil {
		t.Fatal("missing video still playable")
	}
}

func TestBoundDerivedObserverHasJournalReadBoundAndCancellation(t *testing.T) {
	service, task, _, _ := boundDerivedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ReadBoundTask(ctx, task.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	path := filepath.Join(service.Root, task.ID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(boundJournalMaximumBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := service.ReadBoundTask(context.Background(), task.ID); err == nil {
		t.Fatal("oversized bound journal accepted")
	}
}

func TestBoundAuthorityObservesExactOriginalRecordWithoutJournalBody(t *testing.T) {
	service, task, _, _ := boundDerivedFixture(t)
	item := task.Items[0]
	read := func() (Task, error) {
		return service.ReadBoundAuthority(context.Background(), task.ID, item.SourceBindingSHA256, item.BindingAuthoritySHA256)
	}
	if _, err := read(); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(service.Root, task.ID+".json")
	// Existing-token checks observe the immutable creation identity, not mutable
	// generated content. A new open must still reject a damaged full journal.
	if err := os.WriteFile(journal, []byte("not valid task JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if original, err := read(); err != nil || original.Items[0].SourceBindingSHA256 != item.SourceBindingSHA256 {
		t.Fatal("small authority incorrectly reread mutable journal body", err)
	}
	if _, err := service.ReadBoundTask(context.Background(), task.ID); err == nil {
		t.Fatal("new open accepted damaged journal")
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("missing journal remained observable")
	}
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	authority := filepath.Join(service.Root, task.ID+".playback-binding.json")
	before, err := os.Stat(authority)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(authority)
	if err != nil {
		t.Fatal(err)
	}
	// Reordering object keys keeps valid JSON semantics, byte length, and mtime;
	// only an exact byte check can distinguish it from the original authority.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(fields)
	if err != nil || len(reordered) != len(body) || bytes.Equal(reordered, body) {
		t.Fatal("fixture must change valid authority bytes without changing length", err)
	}
	if err := os.WriteFile(authority, reordered, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(authority, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(authority)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("fixture did not preserve authority size and mtime", err)
	}
	if _, err := read(); err == nil {
		t.Fatal("same-size authority bytes accepted with restored mtime")
	}
	// Whitespace preserves JSON meaning but changes the exact immutable bytes.
	body = append(body, ' ')
	if err := os.WriteFile(authority, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(authority, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("changed authority bytes accepted with restored mtime")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ReadBoundAuthority(ctx, task.ID, item.SourceBindingSHA256, item.BindingAuthoritySHA256); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
