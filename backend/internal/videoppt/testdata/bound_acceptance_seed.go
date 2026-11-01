// Isolated acceptance fixture: real production retention factory, caller-supplied
// existing SRT, no downloader/transcriber/provider. Never run on a user library.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
	"strings"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) < 4 {
		panic("usage: isolated-root fixture-video existing-srt [benchmark] [large-journal]")
	}
	root, err := filepath.Abs(os.Args[1])
	must(err)
	must(os.Mkdir(root, 0700)) // New isolated library only; never reuse existing state.
	root, err = filepath.EvalSymlinks(root)
	must(err)
	source, err := filepath.Abs(os.Args[2])
	must(err)
	source, err = filepath.EvalSymlinks(source)
	must(err)
	info, err := os.Stat(source)
	must(err)
	if !info.Mode().IsRegular() || info.Size() > 16*1024*1024 {
		panic("bounded fixture required")
	}
	hash, err := videoppt.VideoDigest(source)
	must(err)
	service := videoppt.New(filepath.Join(root, "VideoTasks"))
	task, err := service.CreateOperationTask("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "Isolated codec fixture", videoppt.VideoOperation{Version: 1, Stage: "subtitle", Path: source, SourceHash: hash, OutputName: "fixture.srt", Page: 1, RetainPlayback: true})
	must(err)
	ctx := context.Background()
	_, err = service.ReservePlaybackRetention(ctx, task.ID, info.Size())
	must(err)
	must(videoppt.CopyVideoReady(ctx, source, task.Path))
	srtBody, err := os.ReadFile(os.Args[3])
	must(err)
	if len(srtBody) > 4000000 {
		panic("bounded existing SRT required")
	}
	srt := string(srtBody)
	segments, err := documents.ParseSrt(srt)
	must(err)
	if len(segments) == 0 {
		panic("no existing cues")
	}
	must(videoppt.SavePreparedSubtitle(task, srt, len(segments)))
	must(service.PreparePlaybackRetention(ctx, task.ID, srt))
	directory := filepath.Join(root, "Transcripts")
	must(os.MkdirAll(directory, 0700))
	saved, err := videoppt.PublishSubtitle(ctx, task, directory, "fixture.srt", []byte(srt))
	must(err)
	must(service.MarkSubtitleSaved(task.ID, saved))
	binding, err := service.CompletePlaybackRetention(ctx, task.ID, saved)
	must(err)
	result := map[string]any{"libraryRoot": root, "videoTaskID": task.ID, "receiptSHA256": binding.RetentionReceiptSHA256, "sourceSHA256": hash, "savedSubtitle": saved, "managedPath": task.Path, "size": info.Size(), "srtBytes": len(srtBody), "cueCount": len(segments)}
	if len(os.Args) > 4 && os.Args[4] == "benchmark" {
		verified, err := service.RetainedSubtitle(ctx, task.ID, binding.RetentionReceiptSHA256, "")
		must(err)
		derived := subtitlederived.New(filepath.Join(root, "NoteGeneration"), root)
		summary, err := derived.CreateRetainedOperation(ctx, verified, "brief", "", "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
		must(err)
		journal := filepath.Join(root, "NoteGeneration", summary.ID+".json")
		if len(os.Args) > 5 && os.Args[5] == "large-journal" {
			summary.Items[0].Prepared = &subtitlederived.Prepared{Markdown: strings.Repeat("x", 64*1024*1024-16384)}
			body, err := json.Marshal(summary)
			must(err)
			must(os.WriteFile(journal, body, 0600))
		}
		stat, err := os.Stat(journal)
		must(err)
		result["journalBytes"] = stat.Size()
		result["boundInput"] = map[string]any{"requestID": "9e326025-30b4-45d0-bd27-4a30275adcc1", "summaryTaskID": summary.ID, "itemIndex": 0, "expectedSourceBindingSHA256": summary.Items[0].SourceBindingSHA256}
	}
	must(json.NewEncoder(os.Stdout).Encode(result))
}
