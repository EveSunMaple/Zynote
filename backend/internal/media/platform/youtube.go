package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func visitorData() string {
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	identifier := make([]byte, 11)
	for index := range identifier {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			identifier[index] = alphabet[0]
			continue
		}
		identifier[index] = alphabet[value.Int64()]
	}
	payload := []byte{0x0a, 0x0b}
	payload = append(payload, identifier...)
	payload = append(payload, 0x28)
	payload = append(payload, varintBytes(uint64(time.Now().UnixMilli()))...)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func varintBytes(value uint64) []byte {
	result := []byte{}
	for {
		byteValue := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			byteValue |= 0x80
		}
		result = append(result, byteValue)
		if value == 0 {
			return result
		}
	}
}

type youtubePost func(ctx context.Context, endpoint string, headers map[string]string, body []byte) (map[string]any, error)

func readYoutubePlaylist(ctx context.Context, listID string, fetcher Fetcher) (media.Catalog, error) {
	return readYoutubePlaylistWith(ctx, listID, defaultYoutubePost)
}

func readYoutubePlaylistWith(ctx context.Context, listID string, post youtubePost) (media.Catalog, error) {
	if post == nil {
		return media.Catalog{}, fmt.Errorf("YouTube 目录读取不可用")
	}
	visitor := visitorData()
	endpoint := "https://www.youtube.com/youtubei/v1/browse?key=" + youtubeAPIKey + "&prettyPrint=false"
	headers := map[string]string{
		"Content-Type":             "application/json",
		"X-Goog-Visitor-Id":        visitor,
		"User-Agent":               "Mozilla/5.0",
		"X-YouTube-Client-Name":    "1",
		"X-YouTube-Client-Version": youtubeClientVersion,
	}
	request := func(body map[string]any) (map[string]any, error) {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return post(ctx, endpoint, headers, encoded)
	}
	contextBody := map[string]any{
		"client": map[string]any{
			"clientName": "WEB", "clientVersion": youtubeClientVersion, "hl": "en", "visitorData": visitor,
		},
	}
	page, err := request(map[string]any{"context": contextBody, "browseId": "VL" + listID})
	if err != nil {
		return media.Catalog{}, err
	}
	title := playlistTitle(page)
	entries := []map[string]string{}
	seen := map[string]bool{}
	collect := func(value any) {
		walkJSON(value, func(node map[string]any) {
			if len(entries) >= 100 {
				return
			}
			if lockup, ok := node["lockupViewModel"].(map[string]any); ok {
				if stringOf(lockup["contentType"]) != "LOCKUP_CONTENT_TYPE_VIDEO" {
					return
				}
				id := stringOf(lockup["contentId"])
				if !youtubeIDPattern.MatchString(id) || seen[id] {
					return
				}
				seen[id] = true
				entries = append(entries, map[string]string{
					"title": truncateRunes(lockupTitle(lockup, id), 240),
					"url":   "https://www.youtube.com/watch?v=" + id,
				})
				return
			}
			if renderer, ok := node["playlistVideoRenderer"].(map[string]any); ok {
				id := stringOf(renderer["videoId"])
				if !youtubeIDPattern.MatchString(id) || seen[id] {
					return
				}
				seen[id] = true
				entries = append(entries, map[string]string{
					"title": truncateRunes(playlistVideoTitle(renderer, id), 240),
					"url":   "https://www.youtube.com/watch?v=" + id,
				})
			}
		})
	}
	collect(page)
	usedTokens := map[string]bool{}
	limited := false
	for len(entries) < 100 {
		token := playlistContinuation(page, usedTokens)
		if token == "" {
			break
		}
		usedTokens[token] = true
		page, err = request(map[string]any{"context": contextBody, "continuation": token})
		if err != nil {
			return media.Catalog{}, err
		}
		before := len(entries)
		collect(page)
		if len(entries) == before {
			limited = true
			break
		}
	}
	if len(entries) >= 100 {
		limited = true
	}
	if len(entries) == 0 {
		return media.Catalog{}, fmt.Errorf("未取得可选择的公开视频")
	}
	if title == "" {
		title = "YouTube 播放列表"
	}
	return media.Catalog{
		URL: "https://www.youtube.com/playlist?list=" + listID, Title: truncateTitle(title),
		Playlist: true, Entries: entries, Limited: limited,
	}, nil
}

func defaultYoutubePost(ctx context.Context, endpoint string, headers map[string]string, body []byte) (map[string]any, error) {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("YouTube 播放列表读取失败（HTTP %d）", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 12*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 12*1024*1024 {
		return nil, fmt.Errorf("YouTube 播放列表响应过大")
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("YouTube 播放列表响应格式异常")
	}
	return value, nil
}

func playlistTitle(page map[string]any) string {
	title := ""
	walkJSON(page, func(node map[string]any) {
		if title != "" {
			return
		}
		if renderer, ok := node["playlistMetadataRenderer"].(map[string]any); ok {
			title = stringOf(renderer["title"])
		}
	})
	if title == "" {
		walkJSON(page, func(node map[string]any) {
			if title != "" {
				return
			}
			if header, ok := node["pageHeaderViewModel"].(map[string]any); ok {
				title = nestedText(header)
			}
		})
	}
	return title
}

func nestedText(value map[string]any) string {
	if text := stringOf(value["content"]); text != "" {
		return text
	}
	if text := stringOf(value["text"]); text != "" {
		return text
	}
	for _, key := range []string{"dynamicTextViewModel", "title", "text"} {
		if child, ok := value[key].(map[string]any); ok {
			if text := nestedText(child); text != "" {
				return text
			}
		}
	}
	return ""
}

func lockupTitle(lockup map[string]any, fallback string) string {
	if metadata, ok := lockup["metadata"].(map[string]any); ok {
		if view, ok := metadata["lockupMetadataViewModel"].(map[string]any); ok {
			if title, ok := view["title"].(map[string]any); ok {
				if text := nestedText(title); text != "" {
					return text
				}
			}
		}
	}
	if metadata, ok := lockup["lockupMetadataViewModel"].(map[string]any); ok {
		if title, ok := metadata["title"].(map[string]any); ok {
			if text := nestedText(title); text != "" {
				return text
			}
		}
	}
	return fallback
}

func playlistVideoTitle(renderer map[string]any, fallback string) string {
	if title, ok := renderer["title"].(map[string]any); ok {
		if runs, ok := title["runs"].([]any); ok && len(runs) > 0 {
			if text := stringOf(objectOf(runs[0])["text"]); text != "" {
				return text
			}
		}
		if text := stringOf(title["simpleText"]); text != "" {
			return text
		}
	}
	return fallback
}

func playlistContinuation(page map[string]any, used map[string]bool) string {
	found := ""
	walkJSON(page, func(node map[string]any) {
		if found != "" {
			return
		}
		if command, ok := node["continuationCommand"].(map[string]any); ok {
			token := stringOf(command["token"])
			if token != "" && !used[token] {
				found = token
			}
		}
	})
	return found
}

func walkJSON(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
