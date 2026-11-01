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
	"time"
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

// 论文 PDF 常超过 2 MB（带插图的可达数十 MB）：不能被一般网页的 2 MB 上限拦下。
func TestArxivPDFAllowsLargerThanGeneralLimit(t *testing.T) {
	parsed, _ := url.Parse("https://arxiv.org/abs/1706.03762v7")
	path, target, body, err := downloadArxivPDF(context.Background(), bodyOfSize(pdfOfSize(20*1024*1024)), parsed)
	if err != nil {
		t.Fatalf("20 MB PDF must be accepted, got %v", err)
	}
	defer os.Remove(path)
	if target != "https://arxiv.org/pdf/1706.03762v7" || len(body) != 20*1024*1024 {
		t.Fatalf("unexpected download %s %d", target, len(body))
	}
}

func TestArxivPDFRejectsAboveLimit(t *testing.T) {
	parsed, _ := url.Parse("https://arxiv.org/abs/1706.03762v7")
	_, _, _, err := downloadArxivPDF(context.Background(), bodyOfSize(pdfOfSize(arxivPDFLimit+1)), parsed)
	if err == nil || !strings.Contains(err.Error(), "50 MB") {
		t.Fatalf("oversized PDF must be rejected with the PDF limit, got %v", err)
	}
}

func TestGeneralResponseKeepsSmallLimit(t *testing.T) {
	_, err := bodyOfSize(pdfOfSize(externalResponseLimit+1)).get(context.Background(), "https://example.com/page")
	if !errors.Is(err, errExternalTooLarge) {
		t.Fatalf("general response above 2 MB must be rejected, got %v", err)
	}
}

// PDF 下载用更长的总时限：同样慢的响应，普通请求按共用客户端的短时限超时，PDF 下载能等到完成；共用客户端本身不被改写。
func TestArxivPDFUsesLongerTimeout(t *testing.T) {
	shared := &http.Client{Timeout: 100 * time.Millisecond}
	shared.Transport = externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(pdfOfSize(64))), Request: request}, nil
	})
	external := &External{Client: shared}
	if _, err := external.get(context.Background(), "https://example.com/slow"); err == nil {
		t.Fatal("general request must still time out with the shared client limit")
	}
	parsed, _ := url.Parse("https://arxiv.org/abs/1706.03762v7")
	path, _, _, err := downloadArxivPDF(context.Background(), external, parsed)
	if err != nil {
		t.Fatalf("slow PDF download must use the longer limit, got %v", err)
	}
	defer os.Remove(path)
	if shared.Timeout != 100*time.Millisecond {
		t.Fatalf("shared client timeout must stay unchanged, got %v", shared.Timeout)
	}
}

// 长论文（超过 64 页）的后部页码不能被悄悄丢掉，也不能因整篇页数多而被拒。
func TestLongPaperPagesAccepted(t *testing.T) {
	choices := parseFulltextChoices([]any{map[string]any{"url": "https://arxiv.org/abs/2112.10752v2", "pages": []any{float64(1), float64(120)}}})
	if len(choices) != 1 || len(choices[0].Pages) != 2 || choices[0].Pages[1] != 120 {
		t.Fatalf("page 120 must be kept: %+v", choices)
	}
	external := &External{PDFPage: func(string, int) (string, int, error) { return "正文", 150, nil }}
	rows, err := arxivEvidenceRows(external, "x.pdf", "https://arxiv.org/pdf/2112.10752v2", "论文", []int{120}, "sha")
	if err != nil || len(rows) != 1 || rows[0].TotalPages != 150 {
		t.Fatalf("150-page paper must be readable: %v %+v", err, rows)
	}
	if _, err := arxivEvidenceRows(external, "x.pdf", "https://arxiv.org/pdf/2112.10752v2", "论文", []int{151}, "sha"); err == nil || !strings.Contains(err.Error(), "共 150 页") {
		t.Fatalf("missing page must say total pages, got %v", err)
	}
}
