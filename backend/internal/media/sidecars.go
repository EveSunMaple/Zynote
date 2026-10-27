package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type mediaSidecar struct {
	relative string
	text     string
}

func mediaSidecarOutputs(description, catalogURL string, tuning map[string]any) []mediaSidecar {
	outputs := []mediaSidecar{}
	if tuning["--write-description"] == true {
		text := description
		if text == "" {
			text = "此视频没有提供简介。"
		}
		outputs = append(outputs, mediaSidecar{relative: "description.txt", text: text + "\n"})
	}
	if tuning["--write-link"] == true {
		escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(catalogURL)
		outputs = append(outputs, mediaSidecar{
			relative: "source.webloc",
			text: "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict><key>URL</key><string>" +
				escaped + "</string></dict></plist>\n",
		})
	}
	return outputs
}

func writeMediaSidecar(base string, output mediaSidecar) error {
	path := filepath.Join(base, output.relative)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
		item, statErr := os.Lstat(path)
		if statErr != nil || !item.Mode().IsRegular() || item.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("附属文件路径无效")
		}
	} else {
		if _, err := file.WriteString(output.text); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != output.text {
		return fmt.Errorf("附属文件与当前来源不一致，保留原文件")
	}
	return nil
}

func MediaSidecars(directory, description, catalogURL string, tuning map[string]any) ([]string, error) {
	base, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, fmt.Errorf("附属文件目录无效")
	}
	info, err := os.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("附属文件目录无效")
	}
	relatives := []string{}
	for _, output := range mediaSidecarOutputs(description, catalogURL, tuning) {
		if err := writeMediaSidecar(base, output); err != nil {
			return nil, err
		}
		relatives = append(relatives, output.relative)
	}
	return relatives, nil
}
