package zhiyun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rewriteTransport struct{ target string }

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme = "http"
	request.URL.Host = transport.target
	return http.DefaultTransport.RoundTrip(request)
}

func TestResourceAndDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/courseapi/v2/course/catalogue":
			fmt.Fprint(writer, `{"success":true,"result":{"code":200,"data":[{"sub_id":"1","title":"第一讲","lecturer_name":"老师","content":{"save_playback":{"contents":"https://onlineroom.cmc.zju.edu.cn/video.mp4"}}}]}}`)
		case "/courseapi/v2/export/get-courseware-status":
			fmt.Fprint(writer, `{"code":0,"data":{"path_name":"https://onlineroom.cmc.zju.edu.cn/file.docx","file_name":"第一讲.docx"}}`)
		case "/file.docx":
			writer.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(writer, "docx-bytes")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(server.URL, Session{})
	client.HTTP = &http.Client{Transport: rewriteTransport{target: server.Listener.Addr().String()}}
	resource, err := client.Resource(context.Background(), "99", "1", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Filename != "第一讲.docx" || resource.URL != "https://onlineroom.cmc.zju.edu.cn/file.docx" {
		t.Fatalf("resource mismatch: %+v", resource)
	}
	path := filepath.Join(t.TempDir(), "download.docx")
	if err := client.Download(context.Background(), resource.URL, path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "docx-bytes" {
		t.Fatalf("download mismatch: %q %v", body, err)
	}
	video, err := client.LessonVideoURL(context.Background(), "99", "1")
	if err != nil || video != "https://onlineroom.cmc.zju.edu.cn/video.mp4" {
		t.Fatalf("video mismatch: %q %v", video, err)
	}
	if _, err := mediaURL("https://onlineroom.cmc.zju.edu.cn", "https://evil.example/x"); err == nil {
		t.Fatal("untrusted media host must fail")
	}
}

func TestResourceWaitsForPendingExport(t *testing.T) {
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/courseapi/v2/course/catalogue":
			fmt.Fprint(writer, `{"success":true,"result":{"code":200,"data":[{"sub_id":"1","title":"第一讲","content":{}}]}}`)
		case "/courseapi/v2/export/get-courseware-status":
			statusCalls++
			if statusCalls == 1 {
				fmt.Fprint(writer, `{"code":0,"data":{}}`)
				return
			}
			fmt.Fprint(writer, `{"code":0,"data":{"path_name":"https://onlineroom.cmc.zju.edu.cn/file.docx","file_name":"第一讲.docx"}}`)
		case "/courseapi/v2/export/export-async":
			fmt.Fprint(writer, `{"code":1,"message":"already queued"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	previous := resourcePollInterval
	resourcePollInterval = time.Millisecond
	defer func() { resourcePollInterval = previous }()

	client := New(server.URL, Session{})
	client.HTTP = &http.Client{Transport: rewriteTransport{target: server.Listener.Addr().String()}}
	resource, err := client.Resource(context.Background(), "99", "1", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Filename != "第一讲.docx" || statusCalls != 2 {
		t.Fatalf("resource mismatch: %+v calls=%d", resource, statusCalls)
	}
}

func TestResourceRejectsVideoURLForPPT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/courseapi/v2/course/catalogue":
			fmt.Fprint(writer, `{"success":true,"result":{"code":200,"data":[{"sub_id":"1","title":"第一讲","content":{}}]}}`)
		case "/courseapi/v2/export/get-courseware-status":
			fmt.Fprint(writer, `{"code":0,"data":{"path_name":"https://onlineroom.cmc.zju.edu.cn/video.mp4","file_name":"第一讲.pptx"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(server.URL, Session{})
	client.HTTP = &http.Client{Transport: rewriteTransport{target: server.Listener.Addr().String()}}
	if _, err := client.Resource(context.Background(), "99", "1", "ppt"); err == nil || !strings.Contains(err.Error(), "视频文件") {
		t.Fatalf("video resource mismatch: %v", err)
	}
}

func TestDownloadLimitedRejectsLargeResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "10")
		fmt.Fprint(writer, "0123456789")
	}))
	defer server.Close()
	client := New(server.URL, Session{})
	client.HTTP = &http.Client{Transport: rewriteTransport{target: server.Listener.Addr().String()}}
	path := filepath.Join(t.TempDir(), "file.pptx")
	err := client.DownloadLimited(context.Background(), "https://onlineroom.cmc.zju.edu.cn/big", path, 5)
	if err == nil || !strings.Contains(err.Error(), "超过允许") {
		t.Fatalf("size limit mismatch: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("oversized file should not be kept")
	}
}
