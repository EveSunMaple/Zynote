package documents

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func buildPptx(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestParsePptx(t *testing.T) {
	data := buildPptx(t, map[string]string{
		"ppt/presentation.xml":             `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/><p:sldId id="2" r:id="r2"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels":  `<Relationships><Relationship Id="r1" Type="x" Target="slides/slide1.xml"/><Relationship Id="r2" Type="x" Target="slides/slide2.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":            `<p:sld><p:cSld><a:t>第一页标题</a:t><a:t>副标题</a:t></p:cSld></p:sld>`,
		"ppt/slides/_rels/slide1.xml.rels": `<Relationships/>`,
		"ppt/slides/slide2.xml":            `<p:sld><a:t>第二页内容</a:t><p:graphicFrame/></p:sld>`,
		"ppt/slides/_rels/slide2.xml.rels": `<Relationships/>`,
	})
	result, err := ParsePptx(data)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.Text, "第一页标题") || !contains(result.Text, "副标题") || !contains(result.Text, "第二页内容") {
		t.Fatalf("pptx text broken: %q", result.Text)
	}
	if !contains(result.Text, "[第 1 页]") || !contains(result.Text, "[第 2 页]") {
		t.Fatalf("page markers broken: %q", result.Text)
	}
	if len(result.Omissions) != 1 || !contains(result.Omissions[0], "第 2 页含图表") {
		t.Fatalf("graphicFrame omission missing: %+v", result.Omissions)
	}
	if result.NeedsOcr {
		t.Fatalf("editable pptx should not need OCR")
	}
}

func TestParsePptxBounds(t *testing.T) {
	small := buildPptx(t, map[string]string{
		"ppt/presentation.xml":            `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships><Relationship Id="r1" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           `<a:t>正常</a:t>`,
	})
	if _, err := ParsePptx(small); err != nil {
		t.Fatal(err)
	}

	noSlides := buildPptx(t, map[string]string{
		"ppt/presentation.xml":            `<p:presentation/>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships/>`,
	})
	if _, err := ParsePptx(noSlides); err == nil || !contains(err.Error(), "1–500") {
		t.Fatalf("slide count error mismatch: %v", err)
	}

	missingEntry := buildPptx(t, map[string]string{
		"ppt/presentation.xml":            `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships><Relationship Id="r1" Target="slides/gone.xml"/></Relationships>`,
	})
	if _, err := ParsePptx(missingEntry); err == nil || !contains(err.Error(), "内容缺失") {
		t.Fatalf("missing slide error mismatch: %v", err)
	}

	escape := buildPptx(t, map[string]string{
		"ppt/presentation.xml":            `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships><Relationship Id="r1" Target="../outside.xml"/></Relationships>`,
	})
	if _, err := ParsePptx(escape); err == nil || !contains(err.Error(), "页面路径无效") {
		t.Fatalf("escape error mismatch: %v", err)
	}
}

func TestParseCoursewareDispatch(t *testing.T) {
	if _, err := ParseCourseware(nil, "/tmp/whatever.ppt"); err == nil || !contains(err.Error(), "另存为 PPTX") {
		t.Fatalf("ppt dispatch error mismatch: %v", err)
	}
	result, err := ParseCourseware(nil, writeTemp(t, "notes.txt", "纯文本内容"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "纯文本内容" || len(result.Images) != 0 || len(result.Omissions) != 0 {
		t.Fatalf("txt courseware broken: %+v", result)
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := writeBytes(path, content); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeBytes(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
