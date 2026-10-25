package api

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

func (inv *Invoker) downloadBiliVideo(store contract.StateStore, ctx context.Context, videoURL, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	reverse := store.Reverse()
	if reverse == nil {
		return contract.Unavailable("视频下载组件不可用")
	}
	if _, err := reverse.Call("shell.snapanyDownload", map[string]any{
		"url": videoURL, "path": destination,
	}, 30*60*1000); err != nil {
		return contract.Unavailable(err.Error())
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	info, err := os.Stat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return contract.Unavailable("视频下载未完成")
	}
	return nil
}

func biliVideoParts(normalized string) (string, string, error) {
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", "", contract.InputInvalid("视频网址无效")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", contract.InputInvalid("视频网址无效")
	}
	page := parsed.Query().Get("p")
	if page == "" {
		page = "1"
	}
	return parts[len(parts)-1], page, nil
}

func biliTaskName(normalized, suffix string) (string, string, string, error) {
	normalized, err := bilibili.VideoURL(normalized)
	if err != nil {
		return "", "", "", err
	}
	videoID, page, err := biliVideoParts(normalized)
	if err != nil {
		return "", "", "", err
	}
	return videoID, page, videoID + "-P" + page + suffix, nil
}
