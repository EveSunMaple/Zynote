package zhiyun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
