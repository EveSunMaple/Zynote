package asr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(body []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(body)
}
func inputDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func srtDigest(srt string) string {
	hash := sha256.Sum256([]byte(srt))
	return hex.EncodeToString(hash[:])
}
func acquisitionPath(pipeline Pipeline) string {
	return filepath.Join(pipeline.Resources, "asr", "onnx-small", "acquisition.json")
}
func observedProvenance(language, acquisition string) *contract.TranscriptionProvenance {
	return &contract.TranscriptionProvenance{Version: 1, RequestedLanguage: language, EngineAdapter: "native-jsonrpc/asr.transcribe", AdapterSchema: 1, RuntimeVersionState: "unknown", DeclaredModelLabel: modelName, ModelIdentityState: "not-attested", AcquisitionSHA256: acquisition}
}
