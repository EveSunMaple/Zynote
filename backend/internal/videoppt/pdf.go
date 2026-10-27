package videoppt

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"image"
	// Register the GIF decoder for image.Decode.
	_ "image/gif"
	// Register the JPEG decoder for image.Decode.
	_ "image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const errPdfSaveCheck = "PDF 保存校验失败"

type Choice struct {
	Page int    `json:"page"`
	Keep bool   `json:"keep"`
	Crop Region `json:"crop"`
}

type ExportResult struct {
	Path     string
	Kept     int
	Removed  int
	FileName string
}

type Picture struct {
	width  int
	height int
	pixels []byte
}

func CropPicture(path string, crop Region) (Picture, error) {
	file, err := os.Open(path)
	if err != nil {
		return Picture{}, err
	}
	defer file.Close()
	source, _, err := image.Decode(file)
	if err != nil {
		return Picture{}, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	left := int(math.Round(float64(width) * crop.Left))
	top := int(math.Round(float64(height) * crop.Top))
	right := int(math.Round(float64(width) * crop.Right))
	bottom := int(math.Round(float64(height) * crop.Bottom))
	cropWidth := right - left
	cropHeight := bottom - top
	if cropWidth < 1 {
		cropWidth = 1
	}
	if cropHeight < 1 {
		cropHeight = 1
	}
	if left+cropWidth > width {
		cropWidth = width - left
	}
	if top+cropHeight > height {
		cropHeight = height - top
	}
	if left < 0 || top < 0 || cropWidth < 1 || cropHeight < 1 {
		return Picture{}, requestError("图片像素数据无效")
	}
	result := Picture{width: cropWidth, height: cropHeight, pixels: make([]byte, cropWidth*cropHeight*3)}
	for y := 0; y < cropHeight; y++ {
		for x := 0; x < cropWidth; x++ {
			red, green, blue, alpha := source.At(bounds.Min.X+left+x, bounds.Min.Y+top+y).RGBA()
			background := 255 * (1 - float64(alpha)/65535)
			index := (y*cropWidth + x) * 3
			result.pixels[index] = clampByte(float64(red)/257 + background)
			result.pixels[index+1] = clampByte(float64(green)/257 + background)
			result.pixels[index+2] = clampByte(float64(blue)/257 + background)
		}
	}
	return result, nil
}

func clampByte(value float64) byte {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return byte(math.Round(value))
}

func deflateBytes(input []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zlib.NewWriter(&buffer)
	if _, err := writer.Write(input); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func BuildPDF(ctx context.Context, images []Picture) ([]byte, error) {
	if len(images) == 0 {
		return nil, requestError("没有可导出的页面")
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	offsets := []int{0}
	object := func(id int, data []byte) {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n", id)
		output.Write(data)
		output.WriteString("\nendobj\n")
	}
	writeObject := func(id int, value string) { object(id, []byte(value)) }
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for index := range images {
		if index > 0 {
			kids += " "
		}
		kids += fmt.Sprintf("%d 0 R", 3+index*3)
	}
	writeObject(2, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(images), kids))
	for index, image := range images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		encoded, err := deflateBytes(image.pixels)
		if err != nil {
			return nil, requestError("图片像素数据无效")
		}
		page := 3 + index*3
		pictureObject := page + 1
		contentObject := page + 2
		writeObject(page, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im %d 0 R >> >> /Contents %d 0 R >>",
			image.width, image.height, pictureObject, contentObject,
		))
		var pictureBuffer bytes.Buffer
		fmt.Fprintf(&pictureBuffer, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", image.width, image.height, len(encoded))
		pictureBuffer.Write(encoded)
		pictureBuffer.WriteString("\nendstream")
		object(pictureObject, pictureBuffer.Bytes())
		stream := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im Do\nQ\n", image.width, image.height)
		content := fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
		writeObject(contentObject, content)
	}
	xref := output.Len()
	count := 3 + len(images)*3
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", count)
	for index := 1; index < count; index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", count, xref)
	return output.Bytes(), nil
}

func VerifyPDF(data []byte, pages int) error {
	if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data, []byte("%%EOF")) {
		return requestError(errPdfSaveCheck)
	}
	count := bytes.Count(data, []byte("/Type /Page "))
	if count != pages {
		return requestError(errPdfSaveCheck)
	}
	return nil
}

func (processor Processor) Export(
	ctx context.Context,
	task CreatedTask,
	choices []Choice,
	outputDirectory string,
) (ExportResult, []byte, error) {
	frames, loaded, err := readFrames(task.Dir)
	if err != nil || !loaded {
		return ExportResult{}, nil, requestError("请先提取并检查课件")
	}
	if len(choices) != len(frames) {
		return ExportResult{}, nil, requestError("页面选择不完整")
	}
	seen := map[int]bool{}
	frameFiles := map[int]string{}
	for _, frame := range frames {
		frameFiles[frame.Page] = filepath.Join(task.Dir, frame.File)
	}
	selected := []Choice{}
	for _, choice := range choices {
		if seen[choice.Page] || frameFiles[choice.Page] == "" || !validCrop(choice.Crop) {
			return ExportResult{}, nil, requestError("页面选择不完整")
		}
		seen[choice.Page] = true
		if choice.Keep {
			selected = append(selected, choice)
		}
	}
	sort.Slice(selected, func(left, right int) bool { return selected[left].Page < selected[right].Page })
	images := make([]Picture, 0, len(selected))
	for _, choice := range selected {
		if err := ctx.Err(); err != nil {
			return ExportResult{}, nil, err
		}
		image, err := CropPicture(frameFiles[choice.Page], choice.Crop)
		if err != nil {
			return ExportResult{}, nil, err
		}
		images = append(images, image)
	}
	data, err := BuildPDF(ctx, images)
	if err != nil {
		return ExportResult{}, nil, err
	}
	if err := VerifyPDF(data, len(images)); err != nil {
		return ExportResult{}, nil, err
	}
	if outputDirectory == "" {
		return ExportResult{}, nil, requestError("导出目录无效")
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return ExportResult{}, nil, requestError("导出目录不可用")
	}
	sourceName := ""
	for _, frame := range frames {
		_ = frame
	}
	rawSource, _ := os.ReadFile(filepath.Join(task.Dir, "source.json"))
	var source Source
	_ = json.Unmarshal(rawSource, &source)
	sourceName = source.Name
	if sourceName == "" {
		sourceName = task.Name
	}
	name := sourceName
	for _, extension := range []string{".mp4", ".mkv", ".mov", ".webm", ".avi"} {
		if len(name) > len(extension) && equalFoldSuffix(name, extension) {
			name = name[:len(name)-len(extension)]
			break
		}
	}
	path, err := reserveExport(outputDirectory, name+"-视频课件.pdf")
	if err != nil {
		return ExportResult{}, nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		_ = os.Remove(path)
		return ExportResult{}, nil, requestError("PDF 保存失败")
	}
	check, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(check, data) {
		_ = os.Remove(path)
		return ExportResult{}, nil, requestError(errPdfSaveCheck)
	}
	if err := VerifyPDF(check, len(images)); err != nil {
		_ = os.Remove(path)
		return ExportResult{}, nil, err
	}
	return ExportResult{Path: path, Kept: len(selected), Removed: len(choices) - len(selected), FileName: name}, data, nil
}

func equalFoldSuffix(value, suffix string) bool {
	if len(value) < len(suffix) {
		return false
	}
	return bytes.EqualFold([]byte(value[len(value)-len(suffix):]), []byte(suffix))
}

func reserveExport(directory, name string) (string, error) {
	extension := filepath.Ext(name)
	stem := name[:len(name)-len(extension)]
	for index := 1; ; index++ {
		candidate := filepath.Join(directory, stem+extension)
		if index > 1 {
			candidate = filepath.Join(directory, fmt.Sprintf("%s (%d)%s", stem, index, extension))
		}
		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			file.Close()
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", requestError("导出文件不可创建")
		}
	}
}
