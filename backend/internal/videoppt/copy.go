package videoppt

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// CopyVideo publishes only a fully copied, synced file. A same-directory hard
// link provides atomic no-overwrite publication on supported filesystems;
// unsupported filesystems fail closed instead of exposing a partial video.
func CopyVideo(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return stageVideo(ctx, input, destination)
}

func stageVideo(ctx context.Context, input io.Reader, destination string) error {
	output, err := os.CreateTemp(filepath.Dir(destination), ".video-copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err := io.Copy(output, &contextVideoReader{ctx: ctx, reader: input}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Link(output.Name(), destination)
}

type contextVideoReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextVideoReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
