package api

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/media/download"
)

// Video learning uses the same authenticated catalog, transfer and verified mux
// as native downloads. SnapAny is no longer part of this path.
func (inv *Invoker) downloadBiliVideo(ctx context.Context, store contract.StateStore, state *contract.State, videoURL, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return err
	}
	reader := bilibili.NewCatalogReader(resources)
	options, err := media.MediaOptions(map[string]any{"container": "mp4"})
	if err != nil {
		return err
	}
	raw, err := reader.Read(videoURL, false, options)
	if err != nil {
		return err
	}
	source, ok := raw.(media.Source)
	if !ok {
		return contract.Unavailable("视频目录不可用")
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return err
	}
	job, err := service.Jobs.Create(source.Identity, source.Catalog.URL, source.Catalog.Title, options, nil)
	if err != nil {
		return err
	}
	runner := download.Runner{Jobs: service.Jobs, Auth: service.Auth, Catalog: reader,
		Mux: &media.NativeMediaMux{Resources: state.ResourcesRoot, AllowSystemFallback: !state.Packaged || os.Getenv("ZYNOTE_SYSTEM_MEDIA") == "1", Platform: contract.CurrentPlatform()},
		Progress: func(_ string, done int64, total *int64) {
			inv.videoProgress(fmt.Sprintf("正在下载视频：%d MB", done/(1024*1024)))
		}}
	result, err := runner.Run(job.ID, ctx)
	if err != nil {
		return err
	}
	output, _ := result["output"].(map[string]any)
	path, _ := output["path"].(string)
	return copyLearningVideo(ctx, path, destination)
}

func copyLearningVideo(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return contract.Unavailable("视频下载未完成")
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, &cancelVideoReader{ctx: ctx, reader: input})
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type cancelVideoReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *cancelVideoReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
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
