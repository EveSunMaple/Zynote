package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/visual"
)

// This is a transport/prompt regression using a generated grid and a controlled
// HTTP response. It does not measure a vision model's timetable accuracy.
func TestVisualRecognitionSendsImageAndGridInstructions(t *testing.T) {
	grid := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 600; x++ {
			c := color.White
			if x%100 == 0 || y%80 == 0 {
				c = color.Black
			}
			grid.Set(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, grid, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "controlled-grid.jpg")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	messages, err := visualRecognitionMessages(visual.Frame{Path: path, Time: 123.5})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad", 400)
			return
		}
		received <- request
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"受控输出：无日期规则，不推断日期。"}}]}`)
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "text-model", VisionModel: "fixture-vision", APIFormat: "chat_completions", Key: "test-only"})
	reply, err := client.Chat(context.Background(), messages, nil)
	if err != nil || reply != "受控输出：无日期规则，不推断日期。" {
		t.Fatal(reply, err)
	}
	request := <-received
	if request["model"] != "fixture-vision" {
		t.Fatal("image did not select vision model", request["model"])
	}
	wireMessages := request["messages"].([]any)
	system := wireMessages[0].(map[string]any)["content"].(string)
	for _, required := range []string{"行列标题", "节次不是课次", "单双周", "不编造日历日期", "不编造起止时间", "不是上课时间"} {
		if !strings.Contains(system, required) {
			t.Fatal("missing relation instruction", required)
		}
	}
	parts := wireMessages[1].(map[string]any)["content"].([]any)
	if !strings.Contains(parts[0].(map[string]any)["text"].(string), "123.5 秒") {
		t.Fatal("frame locator lost")
	}
	imagePart := parts[2].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatal(imagePart)
	}
	url := imagePart["image_url"].(map[string]any)["url"].(string)
	sent, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/jpeg;base64,"))
	if err != nil || !bytes.Equal(sent, encoded.Bytes()) {
		t.Fatal("image bytes changed or missing", err)
	}
	if _, err := visualRecognitionMessages(visual.Frame{Path: path + ".missing"}); err == nil {
		t.Fatal("missing image must not dispatch")
	}
}
