package documents

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestParseSrt(t *testing.T) {
	input := "1\n00:00:01,000 --> 00:00:03,500\n第一句\n\n2\n00:00:03,500 --> 00:00:06,000\n第二句"
	segments, err := ParseSrt(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || segments[0].Start != 1000 || segments[0].End != 3500 || segments[1].Text != "第二句" {
		t.Fatalf("unexpected segments: %+v", segments)
	}

	if _, err := ParseSrt("a\n00:00:01,000 --> 00:00:02,000\n文本"); err == nil || err.Error() != "SRT 字幕缺少序号" {
		t.Fatalf("missing index error mismatch: %v", err)
	}
	if _, err := ParseSrt("1\n00:00:01 --> 00:00:02\n文本"); err == nil || err.Error() != "SRT 时间戳格式错误" {
		t.Fatalf("bad timestamp error mismatch: %v", err)
	}
	if _, err := ParseSrt("1\n00:00:03,000 --> 00:00:02,000\n文本"); err == nil || err.Error() != "字幕为空或时间没有递增" {
		t.Fatalf("regression error mismatch: %v", err)
	}
	if _, err := ParseSrt("2\n00:00:01,000 --> 00:00:02,000\n文本"); err == nil || err.Error() != "字幕序号不连续" {
		t.Fatalf("gap error mismatch: %v", err)
	}
}

func TestMilliseconds(t *testing.T) {
	value, err := milliseconds("01:02:03,456")
	if err != nil || value != 3723456 {
		t.Fatalf("unexpected: %v %v", value, err)
	}
	if _, err := milliseconds("01:99:00,000"); err == nil {
		t.Fatalf("minutes over 59 should fail")
	}
}

func TestSplitTranscript(t *testing.T) {
	input := strings.Join([]string{
		"课程",
		"1. 语音识别",
		"00:00:01 第一句话",
		"00:00:05 第二句话",
		"没有时间戳的句子",
	}, "\n")
	result, err := SplitTranscript(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Estimated || len(result.Segments) != 3 {
		t.Fatalf("unexpected: %+v", result)
	}
	first := result.Segments[0]
	if first.Start != 1000 || first.Text != "第一句话" {
		t.Fatalf("first segment mismatch: %+v", first)
	}
	second := result.Segments[1]
	if second.Start != 5000 || second.End != 6000 {
		t.Fatalf("second segment mismatch: %+v", second)
	}
	third := result.Segments[2]
	if third.Start != 6000 || third.End != 6000+maxInt64(1000, int64(len([]rune("没有时间戳的句子")))*250) {
		t.Fatalf("estimated segment mismatch: %+v", third)
	}

	plain, err := SplitTranscript("与正文无关内容")
	if err != nil || len(plain.Segments) != 1 {
		t.Fatalf("no-stamp block should become one estimated segment: %+v %v", plain, err)
	}

	if _, err := SplitTranscript(""); err == nil || err.Error() != "语音识别原文为空" {
		t.Fatalf("empty transcript error mismatch: %v", err)
	}

	timestamps := strings.Join([]string{"00:00:05 a", "00:00:01 b"}, "\n")
	if _, err := SplitTranscript(timestamps); err == nil || err.Error() != "原文时间戳没有递增，请先检查原文" {
		t.Fatalf("ordering error mismatch: %v", err)
	}
}

func writeDocxZip(t *testing.T, xml string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(xml)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestParseDocx(t *testing.T) {
	xml := `<?xml version="1.0"?><w:document><w:body><w:p><w:r><w:t>第一段</w:t></w:r></w:p><w:br/><w:p><w:r><w:t>&amp;&lt;&gt;第二段</w:t></w:r></w:p></w:body></w:document>`
	text, err := ParseDocx(writeDocxZip(t, xml))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "第一段") || !strings.Contains(text, "第二段") ||
		!strings.Contains(text, "&<>") || !strings.Contains(text, "\n\n") {
		t.Fatalf("docx text broken: %q", text)
	}

	var broken bytes.Buffer
	brokenArchive := zip.NewWriter(&broken)
	brokenFile, err := brokenArchive.Create("word/other.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := brokenFile.Write([]byte("no document here")); err != nil {
		t.Fatal(err)
	}
	if err := brokenArchive.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDocx(broken.Bytes()); err == nil || err.Error() != "Word 原文文件损坏" {
		t.Fatalf("missing document.xml error mismatch: %v", err)
	}
}
