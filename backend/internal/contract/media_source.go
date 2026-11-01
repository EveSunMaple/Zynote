package contract

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var sourceDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sourceBVIDPattern = regexp.MustCompile(`^BV[A-Za-z0-9]{10}$`)
var sourceLocatorPattern = regexp.MustCompile(`^(BV[A-Za-z0-9]{10}|av[1-9][0-9]*)$`)

// MediaSource is an allowlisted public identity projection, never a catalog or
// transfer object. Bili CIDs are supplied only by the authenticated catalog.
type MediaSource struct {
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	URL     string `json:"url,omitempty"`
	VideoID string `json:"videoId,omitempty"`
	BVID    string `json:"bvid,omitempty"`
	Page    int    `json:"page,omitempty"`
	CID     int64  `json:"cid,omitempty"`
}

func (source MediaSource) Valid() bool {
	if source.SHA256 != "" && !sourceDigestPattern.MatchString(source.SHA256) {
		return false
	}
	switch source.Kind {
	case "local":
		return (source.Status == "resolved" || source.Status == "unresolved") && filepath.IsAbs(source.Path) && sourceDigestPattern.MatchString(source.SHA256) && source.URL == "" && source.VideoID == "" && source.BVID == "" && source.Page == 0 && source.CID == 0
	case "bili":
		if source.Path != "" || !sourceLocatorPattern.MatchString(source.VideoID) || source.Page < 1 || source.Page > 99999 || source.URL != fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", source.VideoID, source.Page) {
			return false
		}
		if source.Status == "unresolved" {
			return source.CID == 0 && source.SHA256 == "" && source.BVID == ""
		}
		return source.Status == "resolved" && source.CID > 0 && sourceBVIDPattern.MatchString(source.BVID) && (!strings.HasPrefix(source.VideoID, "BV") || source.VideoID == source.BVID)
	default:
		return false
	}
}
