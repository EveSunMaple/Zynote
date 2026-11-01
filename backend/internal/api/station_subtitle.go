package api

import (
	"context"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) stationSubtitle(ctx context.Context, store contract.StateStore, state *contract.State, task videoppt.CreatedTask) (any, error) {
	inv.projectVideoSource(state, task.ID)
	defer inv.projectVideoSource(state, task.ID)
	service := videoPptService(state)
	prepared, found, err := service.LoadStationSubtitle(task)
	if err != nil {
		return nil, videoPptError(err)
	}
	if !found {
		operation, err := service.Operation(task.ID)
		if err != nil {
			return nil, videoPptError(err)
		}
		subtitle, err := inv.fetchVideoSubtitles(ctx, store, state, operation.URL)
		if err != nil {
			if !bilibili.IsNoSubtitle(err) {
				return nil, err
			}
			prepared = videoppt.StationSubtitle{State: "no-subtitle"}
		} else {
			prepared = videoppt.StationSubtitle{State: "prepared", Title: subtitle.Title, Source: subtitle.Source, Warning: subtitle.Warning, SRT: subtitle.SRT, Count: subtitle.Count}
		}
		if err := service.SaveStationSubtitle(task, prepared); err != nil {
			return nil, videoPptError(err)
		}
		prepared, _, err = service.LoadStationSubtitle(task)
		if err != nil {
			return nil, videoPptError(err)
		}
	}
	if prepared.State == "no-subtitle" {
		if err := inv.ensureQueuedVideo(ctx, store, state, task); err != nil {
			return nil, err
		}
		return inv.transcribeVideoTask(ctx, state, task)
	}
	meta, _, err := service.SubtitleTask(task.ID)
	if err != nil {
		return nil, videoPptError(err)
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", meta.CourseName)
	if err != nil {
		return nil, err
	}
	path, err := videoppt.PublishSubtitle(ctx, task, directory, meta.OutputName, []byte(prepared.SRT))
	if err != nil {
		return nil, videoPptError(err)
	}
	if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
		return nil, videoPptError(err)
	}
	return finishSubtitleTask(state, task, map[string]any{"title": prepared.Title, "page": prepared.Page, "source": prepared.Source, "warning": prepared.Warning, "srt": prepared.SRT, "count": prepared.Count, "savedPath": path}, path)
}
