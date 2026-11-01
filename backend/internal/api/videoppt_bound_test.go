package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestRetainedSummaryAndOldIngressRejectInjectedAuthority(t *testing.T) {
	inv, state, _, _ := retainedAPIFixture(t, true)
	for _, field := range []string{"srt", "title", "path", "sourceBinding", "sourceBindingSHA256", "authority"} {
		params, _ := json.Marshal(map[string]any{"operationId": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "mode": "brief", "retention": map[string]string{"videoTaskID": apiRetentionID, "expectedRetentionReceiptSHA256": "bad"}, field: "injected"})
		if _, err := inv.handleSummaryFromRetained(nil, state, params); err == nil {
			t.Fatal("retained input authority accepted", field)
		}
		old, _ := json.Marshal(map[string]any{"mode": "brief", "srt": apiRetentionSRT, "title": "title", field: "injected"})
		if field != "srt" && field != "title" {
			if _, err := inv.handleVideoSummary(nil, state, old); err == nil {
				t.Fatal("old summary authority accepted", field)
			}
		}
	}
	if _, err := parseSubtitleDerivedInput(json.RawMessage(`{"id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","urls":["https://www.bilibili.com/video/BV1xx411c7mD/"],"modes":["brief"],"sourceBinding":{}}`)); err == nil {
		t.Fatal("start injected authority accepted")
	}
	if _, err := os.Stat(filepath.Join(state.LibraryRoot, "NoteGeneration")); !os.IsNotExist(err) {
		t.Fatal("rejected inputs created note state", err)
	}
}

func TestPrivateBoundPlaybackUsesOriginalAuthorityAndExactSavedSRT(t *testing.T) {
	inv, state, video, _ := retainedAPIFixture(t, true)
	if err := videoppt.SavePreparedSubtitle(video, apiRetentionSRT, 1); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"id": video.ID})
	result, err := inv.handleVideoResumeSubtitleContext(context.Background(), nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	output := result.(map[string]any)
	binding := output["playbackBinding"].(*videoppt.PlaybackBinding)
	verified, err := videoPptService(state).RetainedSubtitle(context.Background(), video.ID, binding.RetentionReceiptSHA256, "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := subtitleDerivedService(state).CreateRetainedOperation(context.Background(), verified, "brief", "", "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	query := map[string]any{"summaryTaskID": task.ID, "itemIndex": 0, "expectedSourceBindingSHA256": task.Items[0].SourceBindingSHA256}
	request, _ := json.Marshal(query)
	declaration, err := inv.handleBoundPlaybackSource(nil, state, request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(declaration)
	var projected map[string]any
	_ = json.Unmarshal(body, &projected)
	if len(projected) != 8 || projected["taskID"] != video.ID {
		t.Fatal("unexpected declaration", projected)
	}
	public := projectSubtitleTask(task)
	if public.Items[0].SourceBinding != nil || public.Items[0].BindingAuthoritySHA256 != "" || public.Items[0].SourceBindingSHA256 == "" {
		t.Fatal("binding locator leaked or reference lost")
	}
	if task.Items[0].SourceBinding == nil {
		t.Fatal("projection mutated journal")
	}
	delete(query, "itemIndex")
	missing, _ := json.Marshal(query)
	if _, err := inv.handleBoundPlaybackSource(nil, state, missing); err == nil {
		t.Fatal("missing item index accepted")
	}
	path := filepath.Join(state.LibraryRoot, filepath.FromSlash(task.Items[0].SourceBinding.SavedSubtitle))
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inv.handleBoundPlaybackSource(nil, state, request); err == nil {
		t.Fatal("changed saved SRT playable")
	}
	if _, err := subtitleDerivedService(state).Read(task.ID); err != nil {
		t.Fatal("live failure invalidated historical identity", err)
	}
}
