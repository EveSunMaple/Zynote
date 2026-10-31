package api

import (
	"context"
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
			state := &contract.State{LibraryRoot: t.TempDir()}
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
			item := inv.batchSubtitleItem(context.Background(), &subtitleStore{state: state}, state, bilibili.VideoEpisode{Page: 1, Title: "lecture", URL: "https://www.bilibili.com/video/BV1234567890/"}, "")
			if item["status"] != "失败" || item["error"] == "" {
				t.Fatal(item)
			}
			if calls > 3 {
				t.Fatal("unexpected reacquisition", calls)
			}
			if _, err := os.Stat(filepath.Join(state.LibraryRoot, "VideoTasks")); !os.IsNotExist(err) {
				t.Fatal("failure initiated video task", err)
			}
		})
	}
}
