package research

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// bodyOfSize 返回固定大小响应体的 mock 客户端。
func bodyOfSize(body []byte) *External {
	return &External{Client: &http.Client{Transport: externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/pdf"}},
			Body: io.NopCloser(bytes.NewReader(body)), Request: request,
		}, nil
	})}}
}

func pdfOfSize(size int) []byte {
	body := bytes.Repeat([]byte{' '}, size)
	copy(body, "%PDF-")
	return body
}

// 常见论文 PDF 在 2–8 MB 之间：不能被一般网页的 2 MB 上限拦下。
func TestArxivPDFAllowsLargerThanGeneralLimit(t *testing.T) {
	parsed, _ := url.Parse("https://arxiv.org/abs/1706.03762v7")
	path, target, body, err := downloadArxivPDF(context.Background(), bodyOfSize(pdfOfSize(3*1024*1024)), parsed)
	if err != nil {
		t.Fatalf("3 MB PDF must be accepted, got %v", err)
	}
	defer os.Remove(path)
	if target != "https://arxiv.org/pdf/1706.03762v7" || len(body) != 3*1024*1024 {
		t.Fatalf("unexpected download %s %d", target, len(body))
	}
}

func TestArxivPDFRejectsAboveLimit(t *testing.T) {
	parsed, _ := url.Parse("https://arxiv.org/abs/1706.03762v7")
	_, _, _, err := downloadArxivPDF(context.Background(), bodyOfSize(pdfOfSize(arxivPDFLimit+1)), parsed)
	if err == nil || !strings.Contains(err.Error(), "8 MB") {
		t.Fatalf("oversized PDF must be rejected with the PDF limit, got %v", err)
	}
}

func TestGeneralResponseKeepsSmallLimit(t *testing.T) {
	_, err := bodyOfSize(pdfOfSize(externalResponseLimit+1)).get(context.Background(), "https://example.com/page")
	if !errors.Is(err, errExternalTooLarge) {
		t.Fatalf("general response above 2 MB must be rejected, got %v", err)
	}
}
