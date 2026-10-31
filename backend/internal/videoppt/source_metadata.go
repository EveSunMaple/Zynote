package videoppt

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func operationSource(operation VideoOperation) (contract.MediaSource, error) {
	if operation.Path != "" {
		source := contract.MediaSource{Kind: "local", Status: "unresolved", Path: operation.Path, SHA256: operation.SourceHash}
		if !source.Valid() {
			return source, requestError("来源身份记录无效")
		}
		return source, nil
	}
	parsed, err := url.Parse(operation.URL)
	if err != nil {
		return contract.MediaSource{}, err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "video" || (parsed.Hostname() != "www.bilibili.com" && parsed.Hostname() != "bilibili.com") || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Opaque != "" {
		return contract.MediaSource{}, requestError("视频来源网址无效")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query["p"]) > 1 {
		return contract.MediaSource{}, requestError("视频分P无效")
	}
	page := 1
	if values, exists := query["p"]; exists {
		raw := values[0]
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > 99999 || strconv.Itoa(page) != raw {
			return contract.MediaSource{}, requestError("视频分P无效")
		}
	}
	source := contract.MediaSource{Kind: "bili", Status: "unresolved", VideoID: parts[1], Page: page, URL: fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", parts[1], page)}
	if !source.Valid() {
		return source, requestError("视频来源身份无效")
	}
	return source, nil
}

func (service *Service) MediaSource(id string) (contract.MediaSource, error) {
	operation, err := service.Operation(id)
	if err != nil {
		return contract.MediaSource{}, err
	}
	source, err := operationSource(operation)
	if err != nil {
		return source, err
	}
	directory, err := service.location(id)
	if err != nil {
		return source, err
	}
	body, err := os.ReadFile(filepath.Join(directory, "media-source.json"))
	if err == nil {
		var resolved contract.MediaSource
		if json.Unmarshal(body, &resolved) != nil || !resolved.Valid() || resolved.Kind != source.Kind || resolved.URL != source.URL || resolved.VideoID != source.VideoID || resolved.Page != source.Page || resolved.Path != source.Path || (source.Kind == "local" && resolved.SHA256 != operation.SourceHash) {
			return source, requestError("来源投影与任务身份不一致")
		}
		source = resolved
	} else if !os.IsNotExist(err) {
		return source, err
	}
	if source.Kind == "local" {
		source.Status = "unresolved"
	} else {
		source.SHA256 = ""
	}
	video := CreatedTask{Dir: directory, Path: filepath.Join(directory, videoFileName)}
	if _, err := os.Stat(filepath.Join(directory, "video-ready.json")); err == nil {
		digest, err := verifiedVideoDigest(video)
		if err != nil {
			return source, err
		}
		if source.Kind == "local" && digest != operation.SourceHash {
			return source, requestError("视频副本与绑定来源不一致")
		}
		if source.Kind == "local" {
			source.Status = "resolved"
		}
		if source.Status == "resolved" {
			source.SHA256 = digest
		}
	} else if !os.IsNotExist(err) {
		return source, err
	}
	return source, nil
}

// ResolveBiliSource persists only catalog identity scalars. No transfer objects,
// signed addresses, headers, captions, descriptions or credentials enter it.
func (service *Service) ResolveBiliSource(id, bvid string, cid int64, page int, identity string) error {
	operation, err := service.Operation(id)
	if err != nil {
		return err
	}
	source, err := operationSource(operation)
	if err != nil {
		return err
	}
	if source.Kind != "bili" || source.Page != page || identity != fmt.Sprintf("%s:%d:%d", bvid, cid, page) {
		return requestError("目录与任务来源身份不一致")
	}
	source.Status = "resolved"
	source.BVID = bvid
	source.CID = cid
	if !source.Valid() {
		return requestError("目录与任务来源身份不一致")
	}
	directory, err := service.location(id)
	if err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(directory, "media-source.json"), source)
}
