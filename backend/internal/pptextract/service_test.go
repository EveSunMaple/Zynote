package pptextract

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func buildDeck(t *testing.T) string {
	t.Helper()
	var imageBytes bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			picture.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	if err := png.Encode(&imageBytes, picture); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deck.pptx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	files := map[string]string{
		"ppt/presentation.xml":             `<p:presentation><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels":  `<Relationships><Relationship Id="rId1" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":            `<p:sld><p:pic r:embed="rId1"></p:pic></p:sld>`,
		"ppt/slides/_rels/slide1.xml.rels": `<Relationships><Relationship Id="rId1" Target="../media/image1.png"/></Relationships>`,
	}
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte(content))
	}
	entry, err := archive.Create("ppt/media/image1.png")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write(imageBytes.Bytes())
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	return path
}

func TestAnalyzeAndExport(t *testing.T) {
	path := buildDeck(t)
	service := New(t.TempDir())
	review, err := service.Analyze(context.Background(), path, "prompt", func(_ context.Context, _, _ string, _ []videoppt.Image) (string, error) {
		return `[{"page":1,"keep":true,"reason":"ok","contentScore":80,"duplicateOf":null}]`, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Pages) != 1 || !review.Pages[0].Keep || review.Pages[0].Width != 20 {
		t.Fatalf("review mismatch: %+v", review)
	}
	result, data, pages, err := service.Export(context.Background(), review.ID, []Choice{{
		Page: 1, Keep: true, Crop: videoppt.Region{Left: 0, Right: 1, Top: 0, Bottom: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 || len(pages) != 1 || !bytes.HasPrefix(data, []byte("%PDF-1.7")) {
		t.Fatalf("export mismatch: %+v", result)
	}
	if _, _, _, err := service.Export(context.Background(), review.ID, nil); err == nil {
		t.Fatal("second export should fail after discard")
	}
}

func TestReadDeckRejectsEditableSlides(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := map[string]string{
		"ppt/presentation.xml":            `<p:presentation><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships><Relationship Id="rId1" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           `<p:sld><a:t>文字</a:t></p:sld>`,
	}
	for name, content := range files {
		entry, _ := archive.Create(name)
		entry.Write([]byte(content))
	}
	archive.Close()
	path := filepath.Join(t.TempDir(), "bad.pptx")
	os.WriteFile(path, buffer.Bytes(), 0o644)
	if _, err := ReadDeck(path, t.TempDir()); err == nil {
		t.Fatal("editable slide must fail")
	}
}
