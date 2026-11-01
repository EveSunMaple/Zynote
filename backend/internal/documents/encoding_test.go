package documents

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

func encodeWith(t *testing.T, codec encoding.Encoding, text string) []byte {
	t.Helper()
	encoded, err := codec.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDecodeTextHandlesCommonEncodings(t *testing.T) {
	chinese := "根管治疗的适应证与禁忌证，牙髓炎的分类与诊断要点。"
	traditional := "根管治療的適應證與禁忌證，牙髓炎的分類與診斷要點，請同學課後複習。"
	ascii := "Lecture 1: endodontic treatment indications and contraindications.\r\n"
	cases := map[string]struct {
		data []byte
		want string
	}{
		"utf-8":           {[]byte(chinese), chinese},
		"utf-16be bom":    {encodeWith(t, unicode.UTF16(unicode.BigEndian, unicode.UseBOM), chinese), chinese},
		"utf-16le no bom": {encodeWith(t, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), ascii), ascii},
		"utf-16be no bom": {encodeWith(t, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), ascii), ascii},
		"gb18030":         {encodeWith(t, simplifiedchinese.GB18030, chinese), chinese},
		"big5":            {encodeWith(t, traditionalchinese.Big5, traditional), traditional},
	}
	for name, item := range cases {
		got, err := DecodeText(item.data)
		if err != nil || got != item.want {
			t.Fatalf("%s: got %q %v", name, got, err)
		}
	}
}

func TestDecodeTextRejectsUnreliableInput(t *testing.T) {
	binary := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1A, 0x00, 0x00, 0xFF, 0x10, 0x00}
	if _, err := DecodeText(binary); !errors.Is(err, ErrTextEncoding) {
		t.Fatalf("binary data should be rejected: %v", err)
	}
	japaneseText := encodeWith(t, japanese.ShiftJIS, "根管治療の適応症と禁忌症について説明します。ノートを確認してください。")
	if text, err := DecodeText(japaneseText); !errors.Is(err, ErrTextEncoding) {
		t.Fatalf("unsupported encodings should be rejected, got %q %v", text, err)
	}
	if got := DecodeTextFile(binary); got != string(binary) {
		t.Fatalf("legacy callers should still get the raw bytes: %q", got)
	}
}

func TestParseDocxRejectsOversizedDocument(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("<w:t>a</w:t>", 1<<16))
	for written := 0; written <= maxDocxXMLBytes; written += len(chunk) {
		if _, err := part.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDocx(archive.Bytes()); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Fatalf("decompression bomb should be rejected: %v", err)
	}
}
