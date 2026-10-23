package media

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var filenameExtensionPattern = regexp.MustCompile(`^[a-z0-9]{2,8}$`)
var filenameTemplatePattern = regexp.MustCompile(`%\((title|id|ext)\)(?:\.(\d{1,3})B|s)`)
var filenameForbiddenChars = regexp.MustCompile(`[\\/<>:"|?*\x00-\x1f]`)
var filenameWhitespace = regexp.MustCompile(`\s+`)
var filenameTrailing = regexp.MustCompile(`[. ]+$`)
var filenameRestrictedChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)
var filenameReserved = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)`)

func utf8Limit(value string, limit int) string {
	result := strings.Builder{}
	used := 0
	for _, char := range value {
		size := len(string(char))
		if used+size > limit {
			break
		}
		result.WriteRune(char)
		used += size
	}
	return result.String()
}

func MediaFilename(options NativeMediaOptions, title, identity, extension string) (string, error) {
	if !filenameExtensionPattern.MatchString(extension) {
		return "", fmt.Errorf("媒体文件扩展名无效")
	}
	fields := map[string]string{"title": title, "id": identity, "ext": extension}
	used := 0
	var renderErr error
	rendered := filenameTemplatePattern.ReplaceAllStringFunc(options.FilenameTemplate, func(match string) string {
		parts := filenameTemplatePattern.FindStringSubmatch(match)
		used++
		if parts[2] != "" {
			limit, err := strconv.Atoi(parts[2])
			if err != nil {
				renderErr = fmt.Errorf("文件名模板包含未支持的字段，仅支持 title、id、ext 和字节长度")
				return ""
			}
			if limit > 200 {
				limit = 200
			}
			return utf8Limit(fields[parts[1]], limit)
		}
		return fields[parts[1]]
	})
	if renderErr != nil {
		return "", renderErr
	}
	if used == 0 || strings.Contains(rendered, "%(") {
		return "", fmt.Errorf("文件名模板包含未支持的字段，仅支持 title、id、ext 和字节长度")
	}
	name := filenameForbiddenChars.ReplaceAllString(rendered, "_")
	name = filenameWhitespace.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)
	name = filenameTrailing.ReplaceAllString(name, "")
	if options.RestrictFilenames {
		name = filenameRestrictedChars.ReplaceAllString(name, "_")
	}
	if filenameReserved.MatchString(name) {
		name = "_" + name
	}
	if name == "" || name == "." || name == ".." || !strings.HasSuffix(strings.ToLower(name), "."+extension) {
		return "", fmt.Errorf("媒体文件名无效")
	}
	suffix := "." + extension
	stem := name[:len(name)-len(suffix)]
	name = utf8Limit(stem, 220-len(suffix)) + suffix
	if name[:len(name)-len(suffix)] == "" {
		return "", fmt.Errorf("媒体文件名不能为空")
	}
	return name, nil
}

func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
