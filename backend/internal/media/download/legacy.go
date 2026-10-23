package download

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

var legacyIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var legacyScopePattern = regexp.MustCompile(`^kbr_[a-f0-9]{32}$`)
var legacyStoragePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)
var legacyBvidPattern = regexp.MustCompile(`^BV[A-Za-z0-9]{10}$`)

var legacyStates = map[string]bool{
	"queued": true, "running": true, "interrupted": true, "cancelled": true,
	"error": true, "completed": true, "forgotten": true,
}

var legacyMediaExtensions = map[string][]string{
	"video":    {".mp4", ".mkv", ".flv", ".webm", ".m4v", ".mov", ".ts"},
	"audio":    {".m4a", ".mp3", ".aac", ".flac", ".ogg", ".opus", ".wav"},
	"subtitle": {".srt", ".vtt", ".ass", ".ssa", ".lrc", ".ttml", ".srv3", ".json3"},
}

type LegacySource struct {
	URL       string `json:"url"`
	StorageID string `json:"storage_id"`
	Bvid      string `json:"bvid"`
	Cid       int64  `json:"cid"`
	Page      int    `json:"page"`
}

type LegacyRecord struct {
	ID           string            `json:"id"`
	KBRootID     string            `json:"kb_root_id"`
	Title        string            `json:"title"`
	State        string            `json:"state"`
	Stage        string            `json:"stage"`
	Progress     media.JobProgress `json:"progress"`
	Error        string            `json:"error"`
	Warning      string            `json:"warning"`
	Source       LegacySource      `json:"source"`
	Options      json.RawMessage   `json:"options"`
	DownloadRoot string            `json:"download_root"`
	Output       string            `json:"output"`
	Files        []string          `json:"files"`
	RecordHash   string            `json:"-"`
}

type LegacyDownloads struct {
	root string
}

func NewLegacyDownloads(root string) *LegacyDownloads {
	return &LegacyDownloads{root: root}
}

func (legacy *LegacyDownloads) scope() (string, error) {
	kb := filepath.Join(legacy.root, "knowledge_base")
	normalized := ""
	if checked, err := checkedLegacyDirectory(kb); err == nil {
		normalized = strings.ToLower(checked)
	} else if errorsIsNotExist(err) {
		absolute, absErr := filepath.Abs(kb)
		if absErr != nil {
			return "", absErr
		}
		normalized = strings.ToLower(absolute)
	} else {
		return "", err
	}
	encoded, _ := json.Marshal([]string{"kbr", normalized})
	sum := sha256.Sum256(encoded)
	return "kbr_" + hex.EncodeToString(sum[:])[:32], nil
}

func (legacy *LegacyDownloads) base() (string, error) {
	records, err := checkedLegacyDirectory(filepath.Join(legacy.root, "records"))
	if err != nil {
		return "", err
	}
	scope, err := legacy.scope()
	if err != nil {
		return "", err
	}
	return checkedLegacyDirectory(filepath.Join(records, scope))
}

func (legacy *LegacyDownloads) Read(id string) (*LegacyRecord, error) {
	if !legacyIDPattern.MatchString(id) {
		return nil, fmt.Errorf("旧下载任务标识无效")
	}
	base, err := legacy.base()
	if err != nil {
		return nil, err
	}
	directory, err := checkedLegacyDirectory(filepath.Join(base, id))
	if err != nil {
		return nil, err
	}
	value, hash, err := readLegacyJSON(filepath.Join(directory, "state.json"), 1024*1024)
	if err != nil {
		return nil, err
	}
	var row LegacyRecord
	if err := json.Unmarshal(value, &row); err != nil {
		return nil, fmt.Errorf("旧下载记录文件无效")
	}
	scope, err := legacy.scope()
	if err != nil {
		return nil, err
	}
	if row.ID != id || row.KBRootID != scope {
		return nil, fmt.Errorf("旧任务来源目录不一致")
	}
	if len([]rune(row.Title)) > 1000 || !legacyStates[row.State] {
		return nil, fmt.Errorf("旧下载记录文件无效")
	}
	if parsed, parseErr := url.Parse(row.Source.URL); parseErr != nil || !parsed.IsAbs() {
		return nil, fmt.Errorf("旧下载记录文件无效")
	}
	if !legacyStoragePattern.MatchString(row.Source.StorageID) {
		return nil, fmt.Errorf("旧下载记录文件无效")
	}
	row.RecordHash = hash
	return &row, nil
}

func (legacy *LegacyDownloads) Unchanged(row *LegacyRecord) error {
	current, err := legacy.Read(row.ID)
	if err != nil {
		return err
	}
	if current.RecordHash != row.RecordHash {
		return fmt.Errorf("读取期间旧下载记录已改变，请重新读取")
	}
	return nil
}

func (legacy *LegacyDownloads) List() ([]map[string]any, error) {
	base, err := legacy.base()
	if err != nil {
		if !errorsIsNotExist(err) {
			return nil, err
		}
		return legacy.listOtherScopes()
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if errorsIsNotExist(err) {
			return legacy.listOtherScopes()
		}
		return nil, err
	}
	rows := []map[string]any{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !legacyIDPattern.MatchString(entry.Name()) {
			continue
		}
		row, err := legacy.Read(entry.Name())
		if err != nil {
			rows = append(rows, map[string]any{
				"id": entry.Name(), "title": "无法读取的旧下载任务", "state": "error",
				"stage": "保留原始记录", "progress": map[string]any{},
				"error_message": "旧任务记录无法核对，未改写或删除；请检查记录来源和格式",
				"warning":       "", "source_url": "", "output": "", "files": []string{},
			})
			continue
		}
		if row.State == "forgotten" {
			continue
		}
		state := row.State
		if state == "queued" || state == "running" {
			state = "interrupted"
		}
		errorMessage := ""
		if row.Error != "" {
			errorMessage = "旧下载未完成，请核对后继续"
		}
		progress := map[string]any{}
		if row.Progress.Downloaded != nil {
			progress["downloaded"] = *row.Progress.Downloaded
		}
		if row.Progress.Total != nil {
			progress["total"] = *row.Progress.Total
		}
		files := row.Files
		if files == nil {
			files = []string{}
		}
		rows = append(rows, map[string]any{
			"id": row.ID, "title": row.Title, "state": state, "stage": row.Stage,
			"progress": progress, "error_message": errorMessage, "warning": row.Warning,
			"source_url": row.Source.URL, "output": row.Output, "files": files,
		})
	}
	return rows, nil
}

func (legacy *LegacyDownloads) listOtherScopes() ([]map[string]any, error) {
	records, err := checkedLegacyDirectory(filepath.Join(legacy.root, "records"))
	if err != nil {
		if errorsIsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(records)
	if err != nil {
		if errorsIsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !legacyScopePattern.MatchString(entry.Name()) {
			continue
		}
		directory, err := checkedLegacyDirectory(filepath.Join(records, entry.Name()))
		if err != nil {
			if errorsIsNotExist(err) {
				return []map[string]any{}, nil
			}
			return nil, err
		}
		children, err := os.ReadDir(directory)
		if err != nil {
			if errorsIsNotExist(err) {
				return []map[string]any{}, nil
			}
			return nil, err
		}
		for _, child := range children {
			if !child.IsDir() || child.Type()&os.ModeSymlink != 0 || !legacyIDPattern.MatchString(child.Name()) {
				continue
			}
			_, statErr := os.Lstat(filepath.Join(directory, child.Name(), "state.json"))
			if statErr == nil {
				return nil, fmt.Errorf("旧下载记录属于另一资料路径，需确认迁移位置；原文件保留")
			}
			if !errorsIsNotExist(statErr) {
				return nil, statErr
			}
		}
	}
	return []map[string]any{}, nil
}

func (legacy *LegacyDownloads) File(id string) (string, error) {
	row, err := legacy.Read(id)
	if err != nil {
		return "", err
	}
	if row.State != "completed" || !filepath.IsAbs(row.DownloadRoot) || !filepath.IsAbs(row.Output) {
		return "", fmt.Errorf("旧任务尚无可确认成果")
	}
	root, err := checkedLegacyDirectory(row.DownloadRoot)
	if err != nil {
		return "", err
	}
	directory, err := checkedLegacyDirectory(row.Output)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Dir(directory), root) {
		return "", fmt.Errorf("旧成果不属于声明的下载目录")
	}
	markerValue, markerHash, err := readLegacyJSON(filepath.Join(directory, ".bilisum-download.json"), 1024*1024)
	if err != nil {
		return "", err
	}
	var manifest struct {
		SchemaVersion int    `json:"schema_version"`
		ManagedBy     string `json:"managed_by"`
		Bvid          string `json:"bvid"`
		OwnershipID   string `json:"ownership_id"`
		DownloadRoot  string `json:"download_root"`
		Directory     string `json:"directory"`
		MediaKind     string `json:"media_kind"`
	}
	if err := json.Unmarshal(markerValue, &manifest); err != nil {
		return "", fmt.Errorf("旧下载成果所有权标记不匹配")
	}
	kind := manifest.MediaKind
	if kind == "" {
		kind = "video"
	}
	if manifest.SchemaVersion != 2 || manifest.ManagedBy != "bilisum" || manifest.Bvid == "" ||
		!legacyIDPattern.MatchString(manifest.OwnershipID) ||
		legacyMediaExtensions[kind] == nil {
		return "", fmt.Errorf("旧下载成果所有权标记不匹配")
	}
	name := strings.ToLower(filepath.Base(directory))
	storage := strings.ToLower(row.Source.StorageID)
	rootCanonical, _ := filepath.Abs(root)
	directoryCanonical, _ := filepath.Abs(directory)
	if manifest.Bvid != row.Source.StorageID ||
		manifest.DownloadRoot != strings.ToLower(rootCanonical) ||
		manifest.Directory != strings.ToLower(directoryCanonical) ||
		(name != storage && !strings.HasSuffix(name, "_"+storage)) {
		return "", fmt.Errorf("旧下载成果所有权标记不匹配")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	primary := false
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return "", fmt.Errorf("旧成果包含未确认文件或链接")
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension == ".part" || extension == ".ytdl" || extension == ".tmp" {
			return "", fmt.Errorf("旧成果仍有未完成分段")
		}
		if containsExtension(legacyMediaExtensions[kind], extension) && info.Size() > 0 {
			primary = true
		}
	}
	if !primary {
		return "", fmt.Errorf("旧成果缺少有效媒体或字幕")
	}
	if err := legacy.Unchanged(row); err != nil {
		return "", err
	}
	_, currentHash, err := readLegacyJSON(filepath.Join(directory, ".bilisum-download.json"), 1024*1024)
	if err != nil || currentHash != markerHash {
		return "", fmt.Errorf("旧成果所有权标记已改变")
	}
	return directory, nil
}

type LegacyRecipe struct {
	Row      *LegacyRecord
	Identity string
	URL      string
	Options  media.NativeMediaOptions
}

func (legacy *LegacyDownloads) Recipe(id string) (*LegacyRecipe, error) {
	row, err := legacy.Read(id)
	if err != nil {
		return nil, err
	}
	if !legacyBvidPattern.MatchString(row.Source.Bvid) || row.Source.Cid <= 0 || row.Source.Page <= 0 {
		return nil, fmt.Errorf("此旧任务需先重新读取站点格式，原记录保留")
	}
	normalized, err := bilibili.VideoURL(row.Source.URL)
	if err != nil {
		return nil, fmt.Errorf("旧任务网址与分集身份不一致，原记录保留")
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, fmt.Errorf("旧任务网址与分集身份不一致，原记录保留")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[1] != row.Source.Bvid {
		return nil, fmt.Errorf("旧任务网址与分集身份不一致，原记录保留")
	}
	page := 1
	if value := parsed.Query().Get("p"); value != "" {
		if _, scanErr := fmt.Sscanf(value, "%d", &page); scanErr != nil || page <= 0 {
			return nil, fmt.Errorf("旧任务网址与分集身份不一致，原记录保留")
		}
	}
	if page != row.Source.Page || row.Source.StorageID != row.Source.Bvid {
		return nil, fmt.Errorf("旧任务网址与分集身份不一致，原记录保留")
	}
	var optionsInput any
	if len(row.Options) > 0 {
		if err := json.Unmarshal(row.Options, &optionsInput); err != nil {
			return nil, fmt.Errorf("旧下载记录文件无效")
		}
	}
	options, err := media.MediaOptions(optionsInput)
	if err != nil {
		return nil, err
	}
	if options.FormatIDs != "" {
		return nil, fmt.Errorf("旧下载器的精确格式编号不可直接沿用，请重新读取格式；原记录保留")
	}
	if options.CookieProfile != "" || options.Privacy || options.UseCookies {
		return nil, fmt.Errorf("旧登录档案不能直接迁移，请在原生下载中重新选择；原记录保留")
	}
	return &LegacyRecipe{
		Row: row, Identity: fmt.Sprintf("%s:%d:%d", row.Source.Bvid, row.Source.Cid, row.Source.Page),
		URL:     fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", row.Source.Bvid, row.Source.Page),
		Options: options,
	}, nil
}

func checkedLegacyDirectory(path string) (string, error) {
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("旧下载目录含链接或路径已改变")
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if strings.ToLower(absolute) != strings.ToLower(target) {
		return "", fmt.Errorf("旧下载目录含链接或路径已改变")
	}
	return target, nil
}

func readLegacyJSON(path string, limit int64) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, "", fmt.Errorf("旧下载记录文件无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(data) > int(limit) {
		return nil, "", fmt.Errorf("旧下载记录文件无效")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, "", fmt.Errorf("旧下载记录文件无效")
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func containsExtension(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, fs.ErrNotExist))
}
