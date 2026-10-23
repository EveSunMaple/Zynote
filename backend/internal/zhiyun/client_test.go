package zhiyun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchAndLessons(t *testing.T) {
	cookie := ""
	authorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie = request.Header.Get("Cookie")
		authorization = request.Header.Get("Authorization")
		switch request.URL.Path {
		case "/userapi/v1/infosimple":
			fmt.Fprint(writer, `{"code":0,"params":{"id":7,"account":"student","tenant_id":112}}`)
		case "/pptnote/v1/searchlist":
			if request.URL.Query().Get("title") != "机器学习" {
				t.Errorf("keyword mismatch: %s", request.URL.RawQuery)
			}
			fmt.Fprint(writer, `{"code":0,"total":{"list":[
				{"course_id":123,"title":"机器学习","lecturer_name":"张老师","term_name":"2026春","c_type":"normal"},
				{"course_id":"123","title":"重复","c_type":"normal"},
				{"course_id":456,"title":"推荐","c_type":"recommend"}
			],"total":21}}`)
		case "/courseapi/v2/course/catalogue":
			fmt.Fprint(writer, `{"success":true,"result":{"code":200,"data":[
				{"sub_id":88,"title":"第一课","lecturer_name":"张老师","content":"{\"save_playback\":{\"contents\":\"https://onlineroom.cmc.zju.edu.cn/video.mp4\"}}"},
				{"id":"89","title":"第二课","content":{"playback":{"url":""}}},
				{"sub_id":"bad"}
			]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(server.URL, Session{Cookie: "_token=abc", Authorization: "Bearer token"})
	search, err := client.Search(context.Background(), "机器学习", 1)
	if err != nil {
		t.Fatal(err)
	}
	courses := search["courses"].([]Course)
	if len(courses) != 1 || courses[0].ID != "123" || courses[0].Teacher != "张老师" || search["hasMore"] != true {
		t.Fatalf("search mismatch: %+v", search)
	}
	if cookie != "_token=abc" || authorization != "Bearer token" {
		t.Fatalf("session headers mismatch: %q %q", cookie, authorization)
	}
	lessons, err := client.Lessons(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 2 || lessons[0].ID != "88" || !lessons[0].VideoAvailable || lessons[1].VideoAvailable {
		t.Fatalf("lessons mismatch: %+v", lessons)
	}
}

func TestErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/userapi/v1/infosimple":
			http.Error(writer, "unauthorized", http.StatusForbidden)
		case "/pptnote/v1/searchlist":
			writer.Header().Set("Content-Type", "text/html")
			fmt.Fprint(writer, "<html>login</html>")
		case "/courseapi/v2/course/catalogue":
			fmt.Fprint(writer, `{"success":false,"result":{"code":500}}`)
		}
	}))
	defer server.Close()
	client := New(server.URL, Session{})
	if _, err := client.Search(context.Background(), "x", 1); err == nil || !strings.Contains(err.Error(), "请先登录") {
		t.Fatalf("login mismatch: %v", err)
	}
	if _, err := client.Search(context.Background(), "x", 1); err == nil {
		t.Fatal("search should fail")
	}
	if _, err := client.Lessons(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "访问权限") {
		t.Fatalf("permission mismatch: %v", err)
	}
}
