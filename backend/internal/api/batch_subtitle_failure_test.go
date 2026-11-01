package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

func TestBatchSubtitleAcquisitionFailureDoesNotDownloadOrTranscribe(t *testing.T) {
	for _, failure := range []string{"login", "rate-limit", "transport", "invalid-track", "untrusted-track", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			state := &contract.State{LibraryRoot: root}
			inv := NewInvoker()
			calls := 0
			inv.biliRoot = filepath.Join(state.LibraryRoot, "MediaDownloads")
			inv.bili = bilibili.NewScopedResources(func(address string, _ map[string]string, _ context.Context) (*http.Response, error) {
				calls++
				status := http.StatusOK
				body := `{"code":0,"data":[{"page":1,"part":"lecture","duration":120,"cid":11}]}`
				if strings.Contains(address, "/x/player/v2") {
					switch failure {
					case "transport":
						return nil, errors.New("controlled transport failure")
					case "cancelled":
						return nil, context.Canceled
					case "login":
						body = `{"code":-101}`
					case "rate-limit":
						status = http.StatusTooManyRequests
					default:
						address := "https://aisubtitle.hdslb.com/track.json"
						if failure == "untrusted-track" {
							address = "https://example.invalid/track.json"
						}
						body = `{"code":0,"data":{"subtitle":{"subtitles":[{"lan":"zh","subtitle_url":"` + address + `"}]}}}`
					}
				} else if strings.Contains(address, "track.json") {
					body = `{"body":[]}`
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			task, _, createErr := inv.createQueuedVideoTask(state, "", "https://www.bilibili.com/video/BV1234567890/", "subtitle-station", "", "", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
			if createErr != nil {
				t.Fatal(createErr)
			}
			params, _ := json.Marshal(map[string]string{"id": task.ID})
			_, runErr := inv.handleVideoResumeSubtitle(&subtitleStore{state: state}, state, params)
			if runErr == nil {
				t.Fatal("failure accepted")
			}
			if calls < 2 || calls > 3 {
				t.Fatal("unexpected reacquisition", calls)
			}
			if _, err := os.Stat(task.Path); !os.IsNotExist(err) {
				t.Fatal("failure initiated video task", err)
			}
			if _, found, err := videoPptService(state).LoadStationSubtitle(task); err != nil || found {
				t.Fatal("failure froze absence decision", found, err)
			}
		})
	}
}

func TestBatchExplicitEmptySubtitleCatalogStillCreatesRecoverableASRTask(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	inv := NewInvoker()
	inv.biliRoot = filepath.Join(root, "MediaDownloads")
	acquisitionCalls := 0
	inv.bili = bilibili.NewScopedResources(func(address string, _ map[string]string, _ context.Context) (*http.Response, error) {
		body := ""
		switch {
		case strings.Contains(address, "/x/player/pagelist"):
			body = `{"code":0,"data":[{"page":1,"part":"lecture","duration":120,"cid":11}]}`
		case strings.Contains(address, "/x/player/v2"):
			body = `{"code":0,"data":{"subtitle":{"subtitles":[]}}}`
		default:
			acquisitionCalls++
			return nil, errors.New("controlled stop before media acquisition")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	task, _, createErr := inv.createQueuedVideoTask(state, "", "https://www.bilibili.com/video/BV1234567890/", "subtitle-station", "", "", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	if createErr != nil {
		t.Fatal(createErr)
	}
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	_, runErr := inv.handleVideoResumeSubtitle(&subtitleStore{state: state}, state, params)
	if runErr == nil || acquisitionCalls != 1 {
		t.Fatal(runErr, acquisitionCalls)
	}
	pending, err := videoPptService(state).Pending()
	if err != nil || len(pending) != 1 || pending[0].Kind != "subtitle" {
		t.Fatal("ASR recovery task missing", pending, err)
	}
}
