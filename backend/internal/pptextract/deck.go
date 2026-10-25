package pptextract

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

type Page struct {
	Page        int
	Width       int
	Height      int
	Hash        string
	Preview     string
	Crop        videoppt.Region
	ImagePath   string
	Unavailable string
}

type Deck struct {
	Pages []Page
}

var (
	slideIDPattern  = regexp.MustCompile(`<p:sldId\b[^>]*\br:id=["']([^"']+)["'][^>]*\/?\s*>`)
	relationshipPat = regexp.MustCompile(`<Relationship\b[^>]*\/?\s*>`)
	attrPat         = func(name string) *regexp.Regexp { return regexp.MustCompile(`\b` + name + `=["']([^"']+)["']`) }
	picturePattern  = regexp.MustCompile(`(?s)<p:pic\b.*?</p:pic>`)
	editablePat     = regexp.MustCompile(`<a:t\b|<p:graphicFrame\b|<p:sp\b`)
	srcRectPattern  = regexp.MustCompile(`<a:srcRect\b[^>]*\/?\s*>`)
	transformPat    = regexp.MustCompile(`<a:xfrm\b[^>]*>`)
)

func decodeEntities(value string) string {
	value = strings.ReplaceAll(value, "&lt;", "<")
	value = strings.ReplaceAll(value, "&gt;", ">")
	value = strings.ReplaceAll(value, "&quot;", `"`)
	value = strings.ReplaceAll(value, "&apos;", "'")
	return strings.ReplaceAll(value, "&amp;", "&")
}

func attrValue(xml, name string) string {
	match := attrPat(name).FindStringSubmatch(xml)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func partTarget(parent, value string) string {
	target := decodeEntities(value)
	if strings.HasPrefix(target, "/") {
		return path.Clean(target[1:])
	}
	return path.Clean(path.Join(parent, target))
}

func entryData(archive *zip.Reader, name string, limit int64) ([]byte, error) {
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		buffer := bytes.Buffer{}
		_, err = buffer.ReadFrom(reader)
		if err != nil || int64(buffer.Len()) > limit {
			return nil, requestError("课件图片超过读取上限")
		}
		return buffer.Bytes(), nil
	}
	return nil, os.ErrNotExist
}

func cropImage(source image.Image, region videoppt.Region) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	left := int(float64(width) * region.Left)
	top := int(float64(height) * region.Top)
	right := int(float64(width) * region.Right)
	bottom := int(float64(height) * region.Bottom)
	if right <= left {
		right = left + 1
	}
	if bottom <= top {
		bottom = top + 1
	}
	result := image.NewRGBA(image.Rect(0, 0, right-left, bottom-top))
	draw.Draw(result, result.Bounds(), source, image.Pt(bounds.Min.X+left, bounds.Min.Y+top), draw.Src)
	return result
}

func trimBlackSides(source image.Image) videoppt.Region {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 {
		return videoppt.Region{Left: 0, Right: 1, Top: 0, Bottom: 1}
	}
	black := func(x int) bool {
		count := 0
		for y := 0; y < height; y++ {
			red, green, blue, alpha := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			if red < 42*257 && green < 42*257 && blue < 42*257 && alpha > 200*257 {
				count++
			}
		}
		return float64(count)/float64(height) > 0.999
	}
	allBlack := true
	for x := 0; x < width; x++ {
		if !black(x) {
			allBlack = false
			break
		}
	}
	if allBlack {
		return videoppt.Region{Left: 0, Right: 1, Top: 0, Bottom: 1}
	}
	dark, sampled, stride := 0, 0, int(float64(minInt(width, height))/160)
	if stride < 1 {
		stride = 1
	}
	for y := int(float64(height) * 0.1); y < int(float64(height)*0.9); y += stride {
		for x := int(float64(width) * 0.15); x < int(float64(width)*0.85); x += stride {
			red, green, blue, alpha := source.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			sampled++
			if red < 65*257 && green < 65*257 && blue < 65*257 && alpha > 200*257 {
				dark++
			}
		}
	}
	if sampled > 0 && float64(dark)/float64(sampled) > 0.45 {
		return videoppt.Region{Left: 0, Right: 1, Top: 0, Bottom: 1}
	}
	left, right := 0, width-1
	for left < int(float64(width)*0.35) && black(left) {
		left++
	}
	for right > int(float64(width)*0.65) && black(right) {
		right--
	}
	return videoppt.Region{Left: float64(left) / float64(width), Right: float64(right+1) / float64(width), Top: 0, Bottom: 1}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func ReadDeck(sourcePath, outputDirectory string) (Deck, error) {
	absolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return Deck{}, requestError("请选择图片型 PPTX；旧版 PPT 请先另存为 PPTX")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || strings.ToLower(filepath.Ext(absolute)) != ".pptx" {
		return Deck{}, requestError("请选择图片型 PPTX；旧版 PPT 请先另存为 PPTX")
	}
	if info.Size() > 200*1024*1024 {
		return Deck{}, requestError("请选择小于 200 MB 的 PPTX")
	}
	archive, err := zip.OpenReader(absolute)
	if err != nil {
		return Deck{}, requestError("无法读取 PPTX")
	}
	defer archive.Close()
	presentationRaw, err := entryData(&archive.Reader, "ppt/presentation.xml", 5*1024*1024)
	if err != nil {
		return Deck{}, requestError("无法读取 PPTX 页面目录")
	}
	relsRaw, err := entryData(&archive.Reader, "ppt/_rels/presentation.xml.rels", 5*1024*1024)
	if err != nil {
		return Deck{}, requestError("无法读取 PPTX 页面目录")
	}
	ids := []string{}
	for _, match := range slideIDPattern.FindAllStringSubmatch(string(presentationRaw), -1) {
		ids = append(ids, match[1])
	}
	if len(ids) == 0 || len(ids) > 500 {
		return Deck{}, requestError("请选择包含 1–500 页的图片型 PPTX")
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return Deck{}, requestError("任务目录不可创建")
	}
	pages := []Page{}
	compressedBytes := 0
	for index, id := range ids {
		slideTarget := ""
		for _, row := range relationshipPat.FindAllString(string(relsRaw), -1) {
			if attrValue(row, "Id") == id {
				slideTarget = attrValue(row, "Target")
				break
			}
		}
		name := partTarget("ppt", slideTarget)
		if slideTarget == "" || !strings.HasPrefix(name, "ppt/slides/") {
			return Deck{}, requestError("课件页面关系无效")
		}
		slideRaw, err := entryData(&archive.Reader, name, 5*1024*1024)
		if err != nil {
			return Deck{}, requestError("课件页面缺失")
		}
		xml := string(slideRaw)
		pictures := picturePattern.FindAllString(xml, -1)
		if len(pictures) == 0 && !editablePat.MatchString(xml) {
			continue
		}
		if len(pictures) != 1 || editablePat.MatchString(xml) {
			return Deck{}, requestError("第 " + itoa(index+1) + " 页包含可编辑对象或多张图片，暂不能完整处理；请先将每页转换为一张图片后再导入")
		}
		embed := attrValue(pictures[0], "r:embed")
		relsName := path.Join(path.Dir(name), "_rels", path.Base(name)+".rels")
		slideRelsRaw, err := entryData(&archive.Reader, relsName, 5*1024*1024)
		if err != nil {
			return Deck{}, requestError("第 " + itoa(index+1) + " 页没有可读取的内嵌图片")
		}
		mediaTarget := ""
		for _, row := range relationshipPat.FindAllString(string(slideRelsRaw), -1) {
			if attrValue(row, "Id") == embed {
				mediaTarget = attrValue(row, "Target")
				break
			}
		}
		mediaName := partTarget(path.Dir(name), mediaTarget)
		if mediaTarget == "" || !strings.HasPrefix(mediaName, "ppt/media/") {
			return Deck{}, requestError("第 " + itoa(index+1) + " 页没有可读取的内嵌图片")
		}
		data, err := entryData(&archive.Reader, mediaName, 40*1024*1024)
		if err != nil || len(data) == 0 {
			pages = append(pages, Page{Page: index + 1, Unavailable: "原课件内嵌图片缺失，无法恢复；已保留原文件"})
			continue
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			pages = append(pages, Page{Page: index + 1, Unavailable: "原课件内嵌图片损坏或格式不支持，无法读取；已保留原文件"})
			continue
		}
		if decoded.Bounds().Dx()*decoded.Bounds().Dy() > 25000000 {
			return Deck{}, requestError("单页图片分辨率过大，请降低分辨率")
		}
		rect := srcRectPattern.FindString(pictures[0])
		source := decoded
		if rect != "" {
			region := videoppt.Region{
				Left: parsePercent(attrValue(rect, "l")), Top: parsePercent(attrValue(rect, "t")),
				Right: 1 - parsePercent(attrValue(rect, "r")), Bottom: 1 - parsePercent(attrValue(rect, "b")),
			}
			source = cropImage(source, region)
		}
		transform := transformPat.FindString(pictures[0])
		if transform != "" && (attrValue(transform, "rot") != "" && attrValue(transform, "rot") != "0" ||
			attrValue(transform, "flipH") == "1" || attrValue(transform, "flipV") == "1" ||
			attrValue(transform, "flipH") == "true" || attrValue(transform, "flipV") == "true") {
			return Deck{}, requestError("第 " + itoa(index+1) + " 页图片带旋转或翻转，请先转换为完整截图")
		}
		crop := trimBlackSides(source)
		imagePath := filepath.Join(outputDirectory, "page-"+itoa(index+1)+".png")
		file, err := os.Create(imagePath)
		if err != nil {
			return Deck{}, requestError("任务目录不可创建")
		}
		if err := png.Encode(file, source); err != nil {
			file.Close()
			return Deck{}, requestError("课件图片无法保存")
		}
		file.Close()
		stored, _ := os.ReadFile(imagePath)
		compressedBytes += len(stored)
		if compressedBytes > 200*1024*1024 {
			return Deck{}, requestError("课件解压后的图片超过 200 MB，请压缩图片后重试")
		}
		sum := sha256.Sum256(stored)
		preview, err := videoppt.PreviewJPEG(imagePath, 900, 82)
		if err != nil {
			return Deck{}, requestError("课件图片无法读取")
		}
		bounds := source.Bounds()
		pages = append(pages, Page{
			Page: index + 1, Width: bounds.Dx(), Height: bounds.Dy(), Hash: hex.EncodeToString(sum[:]),
			Preview: preview, Crop: crop, ImagePath: imagePath,
		})
	}
	available := false
	for _, page := range pages {
		if page.Unavailable == "" {
			available = true
			break
		}
	}
	if !available {
		return Deck{}, requestError("课件中没有可读取的图片页；原课件图片缺失或损坏")
	}
	return Deck{Pages: pages}, nil
}

func parsePercent(value string) float64 {
	if value == "" {
		return 0
	}
	number := 0.0
	for _, char := range value {
		if char < '0' || char > '9' {
			break
		}
		number = number*10 + float64(char-'0')
	}
	return number / 100000
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
