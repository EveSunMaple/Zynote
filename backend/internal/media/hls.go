package media

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type HlsKey struct {
	URL string `json:"url"`
	IV  string `json:"iv,omitempty"`
}

type HlsVariant struct {
	URL       string
	Height    int
	Bandwidth float64
	Codec     string
	Audio     string
}

type HlsAudio struct {
	URL       string
	Group     string
	Preferred bool
}

type HlsSegment struct {
	URL      string
	Duration float64
	Sequence uint64
	Key      *HlsKey
}

type HlsManifest struct {
	Variants []HlsVariant
	Audio    []HlsAudio
	Segments []HlsSegment
	Init     string
}

var hlsAttrPattern = regexp.MustCompile(`([A-Z0-9-]+)=("[^"]*"|[^,]*)(?:,|$)`)
var hlsSequencePattern = regexp.MustCompile(`^\d{1,20}$`)
var hlsIVPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{32}$`)

func hlsAttrs(line string) (map[string]string, error) {
	index := strings.Index(line, ":")
	if index < 0 {
		return nil, fmt.Errorf("HLS 属性无效")
	}
	raw := line[index+1:]
	result := map[string]string{}
	consumed := 0
	for _, match := range hlsAttrPattern.FindAllStringSubmatchIndex(raw, -1) {
		if match[0] != consumed {
			return nil, fmt.Errorf("HLS 属性无效")
		}
		key := raw[match[2]:match[3]]
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("HLS 属性无效")
		}
		value := raw[match[4]:match[5]]
		value = strings.TrimPrefix(value, `"`)
		value = strings.TrimSuffix(value, `"`)
		result[key] = value
		consumed = match[1]
	}
	if consumed != len(raw) {
		return nil, fmt.Errorf("HLS 属性格式不支持")
	}
	return result, nil
}

func hlsResolve(base, raw string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("HLS 清单地址无效")
	}
	rawURL, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("HLS 清单地址无效")
	}
	return baseURL.ResolveReference(rawURL).String(), nil
}

func hlsLines(text string) ([]string, error) {
	normalized := strings.TrimPrefix(text, "\ufeff")
	rawLines := strings.Split(strings.ReplaceAll(normalized, "\r\n", "\n"), "\n")
	lines := []string{}
	for _, line := range rawLines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 || lines[0] != "#EXTM3U" || len(lines) > 50000 {
		return nil, fmt.Errorf("HLS 清单无效或过大")
	}
	return lines, nil
}

type hlsParser struct {
	manifest HlsManifest
	base     string
	check    func(string) (string, error)
	pending  map[string]string
	duration *float64
	sequence uint64
	key      *HlsKey
	initURL  string
	ended    bool
}

func (parser *hlsParser) resolve(raw string) (string, error) {
	resolved, err := hlsResolve(parser.base, raw)
	if err != nil {
		return "", err
	}
	return parser.check(resolved)
}

func (parser *hlsParser) parseMedia(line string) error {
	attrs, err := hlsAttrs(line)
	if err != nil {
		return err
	}
	if attrs["TYPE"] != "AUDIO" || attrs["URI"] == "" {
		return nil
	}
	resolved, err := parser.resolve(attrs["URI"])
	if err != nil {
		return err
	}
	parser.manifest.Audio = append(parser.manifest.Audio, HlsAudio{
		URL: resolved, Group: attrs["GROUP-ID"], Preferred: attrs["DEFAULT"] == "YES",
	})
	return nil
}

func (parser *hlsParser) parseSequence(line string) error {
	value := strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")
	if !hlsSequencePattern.MatchString(value) {
		return fmt.Errorf("HLS 序号无效")
	}
	sequence, _ := strconv.ParseUint(value, 10, 64)
	parser.sequence = sequence
	return nil
}

func (parser *hlsParser) parseDuration(line string) error {
	raw := strings.SplitN(strings.TrimPrefix(line, "#EXTINF:"), ",", 2)[0]
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || parsed <= 0 || parsed > 3600 {
		return fmt.Errorf("HLS 片段时间无效")
	}
	parser.duration = &parsed
	return nil
}

func (parser *hlsParser) parseKey(line string) error {
	attrs, err := hlsAttrs(line)
	if err != nil {
		return err
	}
	if attrs["METHOD"] == "NONE" {
		parser.key = nil
		return nil
	}
	if attrs["METHOD"] != "AES-128" || attrs["URI"] == "" ||
		(attrs["KEYFORMAT"] != "" && attrs["KEYFORMAT"] != "identity") ||
		(attrs["IV"] != "" && !hlsIVPattern.MatchString(attrs["IV"])) {
		return fmt.Errorf("此 HLS 加密格式尚不支持")
	}
	resolved, err := parser.resolve(attrs["URI"])
	if err != nil {
		return err
	}
	parser.key = &HlsKey{URL: resolved, IV: attrs["IV"]}
	return nil
}

func (parser *hlsParser) parseMap(line string) error {
	attrs, err := hlsAttrs(line)
	if err != nil {
		return err
	}
	if attrs["URI"] == "" || attrs["BYTERANGE"] != "" || parser.key != nil {
		return fmt.Errorf("此 HLS 初始化范围或加密格式尚不支持")
	}
	next, err := parser.resolve(attrs["URI"])
	if err != nil {
		return err
	}
	if parser.initURL != "" && parser.initURL != next {
		return fmt.Errorf("HLS 初始化轨道改变，请使用独立片段任务")
	}
	parser.initURL = next
	return nil
}

func hlsVariantHeight(resolution string) (int, error) {
	if resolution == "" {
		return 0, nil
	}
	parts := strings.Split(resolution, "x")
	if len(parts) != 2 {
		return 0, nil
	}
	height, err := strconv.Atoi(parts[1])
	if err != nil || height < 0 {
		return 0, fmt.Errorf("HLS 格式目录无效")
	}
	return height, nil
}

func hlsVariantBandwidth(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || parsed < 0 {
		return 0, fmt.Errorf("HLS 格式目录无效")
	}
	return parsed, nil
}

func (parser *hlsParser) parseVariant(line string) error {
	height, err := hlsVariantHeight(parser.pending["RESOLUTION"])
	if err != nil {
		return err
	}
	bandwidth, err := hlsVariantBandwidth(parser.pending["BANDWIDTH"])
	if err != nil {
		return err
	}
	resolved, err := parser.resolve(line)
	if err != nil {
		return err
	}
	parser.manifest.Variants = append(parser.manifest.Variants, HlsVariant{
		URL: resolved, Height: height, Bandwidth: bandwidth,
		Codec: parser.pending["CODECS"], Audio: parser.pending["AUDIO"],
	})
	parser.pending = nil
	return nil
}

func (parser *hlsParser) parseSegment(line string) error {
	if parser.duration == nil || parser.sequence == math.MaxUint64 {
		return fmt.Errorf("HLS 片段缺少时间或序号")
	}
	resolved, err := parser.resolve(line)
	if err != nil {
		return err
	}
	parser.manifest.Segments = append(parser.manifest.Segments, HlsSegment{
		URL: resolved, Duration: *parser.duration, Sequence: parser.sequence, Key: parser.key,
	})
	parser.sequence++
	parser.duration = nil
	if len(parser.manifest.Segments) > 10000 {
		return fmt.Errorf("HLS 片段超过本次有界任务上限")
	}
	return nil
}

func (parser *hlsParser) parseURI(line string) error {
	if parser.pending != nil {
		return parser.parseVariant(line)
	}
	return parser.parseSegment(line)
}

func (parser *hlsParser) parseLine(line string) error {
	switch {
	case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
		attrs, err := hlsAttrs(line)
		if err != nil {
			return err
		}
		parser.pending = attrs
	case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
		return parser.parseMedia(line)
	case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
		return parser.parseSequence(line)
	case strings.HasPrefix(line, "#EXTINF:"):
		return parser.parseDuration(line)
	case strings.HasPrefix(line, "#EXT-X-KEY:"):
		return parser.parseKey(line)
	case strings.HasPrefix(line, "#EXT-X-MAP:"):
		return parser.parseMap(line)
	case strings.HasPrefix(line, "#EXT-X-BYTERANGE:") || strings.HasPrefix(line, "#EXT-X-GAP"):
		return fmt.Errorf("此 HLS 字节范围或缺失片段尚不支持")
	case line == "#EXT-X-ENDLIST":
		parser.ended = true
	case !strings.HasPrefix(line, "#"):
		return parser.parseURI(line)
	}
	return nil
}

func ParseHls(text, base string, check func(string) (string, error)) (HlsManifest, error) {
	lines, err := hlsLines(text)
	if err != nil {
		return HlsManifest{}, err
	}
	parser := &hlsParser{
		manifest: HlsManifest{Variants: []HlsVariant{}, Audio: []HlsAudio{}, Segments: []HlsSegment{}},
		base:     base,
		check:    check,
	}
	for _, line := range lines[1:] {
		if err := parser.parseLine(line); err != nil {
			return parser.manifest, err
		}
	}
	manifest := parser.manifest
	if parser.pending != nil || parser.duration != nil || (len(manifest.Variants) > 0 && len(manifest.Segments) > 0) ||
		len(manifest.Variants) > 200 {
		return manifest, fmt.Errorf("HLS 清单结构不完整")
	}
	if len(manifest.Variants) == 0 && (!parser.ended || len(manifest.Segments) == 0) {
		return manifest, fmt.Errorf("仅支持已结束且完整的 HLS 录像，未把直播窗口当作完整视频")
	}
	manifest.Init = parser.initURL
	return manifest, nil
}

type HlsInput struct {
	Directory string
	Key       string
	Identity  string
	URL       string
	Options   NativeMediaOptions
	Headers   map[string]string
	Ctx       context.Context // NOSONAR: request-scoped input struct; the caller owns this context for a single operation.
	Fetch     func(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error)
	Check     func(string) (string, error)
	Progress  func(downloaded int64, total *int64)
}

type HlsTrack struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type HlsResult struct {
	Tracks        []HlsTrack `json:"tracks"`
	Intermediates []string   `json:"intermediates"`
}

type hlsTrackRef struct {
	URL  string
	Kind string
}

type hlsPart struct {
	URL         string
	Key         *HlsKey
	Sequence    uint64
	HasSequence bool
}

type hlsProof struct {
	Path        string
	Hash        string
	Key         *HlsKey
	Sequence    uint64
	HasSequence bool
}

var hlsKeyPattern = regexp.MustCompile(`^track-\d+$`)

func readHlsManifest(input HlsInput, check func(string) (string, error), requestURL string) (HlsManifest, error) {
	checked, err := check(requestURL)
	if err != nil {
		return HlsManifest{}, err
	}
	response, err := input.Fetch(checked, input.Headers, input.Ctx)
	if err != nil {
		return HlsManifest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return HlsManifest{}, fmt.Errorf("HLS 清单未取得")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return HlsManifest{}, fmt.Errorf("HLS 清单未取得")
	}
	if len(raw) > 2*1024*1024 {
		return HlsManifest{}, fmt.Errorf("HLS 清单超过读取上限")
	}
	return ParseHls(string(raw), requestURL, check)
}

func hlsCodecPrefix(codec string) string {
	switch codec {
	case "h264":
		return "avc"
	case "h265":
		return "hev"
	case "vp9":
		return "vp"
	case "av1":
		return "av01"
	}
	return ""
}

func hlsCandidates(variants []HlsVariant, options NativeMediaOptions) ([]HlsVariant, error) {
	prefix := hlsCodecPrefix(options.VideoCodec)
	candidates := []HlsVariant{}
	for _, variant := range variants {
		if options.Height != 0 && (variant.Height <= 0 || variant.Height > options.Height) {
			continue
		}
		if prefix != "" && !strings.Contains(variant.Codec, prefix) {
			continue
		}
		candidates = append(candidates, variant)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("HLS 没有符合画质和编码要求的格式")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Height != candidates[j].Height {
			return candidates[i].Height > candidates[j].Height
		}
		return candidates[i].Bandwidth > candidates[j].Bandwidth
	})
	return candidates, nil
}

func selectHlsAudio(audio []HlsAudio, group string) *HlsAudio {
	var selected *HlsAudio
	for index := range audio {
		if audio[index].Group != group {
			continue
		}
		if selected == nil || (audio[index].Preferred && !selected.Preferred) {
			selected = &audio[index]
		}
	}
	return selected
}

func resolveHlsTracks(input HlsInput, check func(string) (string, error), requestURL string, depth int) ([]hlsTrackRef, error) {
	if depth > 2 {
		return nil, fmt.Errorf("HLS 目录嵌套过多")
	}
	manifest, err := readHlsManifest(input, check, requestURL)
	if err != nil {
		return nil, err
	}
	if len(manifest.Variants) == 0 {
		return []hlsTrackRef{{URL: requestURL, Kind: "combined"}}, nil
	}
	candidates, err := hlsCandidates(manifest.Variants, input.Options)
	if err != nil {
		return nil, err
	}
	picked := candidates[0]
	audio := selectHlsAudio(manifest.Audio, picked.Audio)
	paths, err := resolveHlsTracks(input, check, picked.URL, depth+1)
	if err != nil {
		return nil, err
	}
	if audio == nil {
		return paths, nil
	}
	if len(paths) != 1 {
		return nil, fmt.Errorf("HLS 音轨嵌套有歧义")
	}
	return []hlsTrackRef{
		{URL: paths[0].URL, Kind: "video"},
		{URL: audio.URL, Kind: "audio"},
	}, nil
}

func downloadHlsTrack(input HlsInput, check func(string) (string, error), keys map[string][]byte,
	track hlsTrackRef, trackIndex int, downloaded *int64, intermediates []string) (HlsTrack, []string, error) {
	manifest, err := readHlsManifest(input, check, track.URL)
	if err != nil {
		return HlsTrack{}, intermediates, err
	}
	if len(manifest.Variants) > 0 {
		return HlsTrack{}, intermediates, fmt.Errorf("HLS 音轨需要具体媒体清单")
	}
	parts := []hlsPart{}
	if manifest.Init != "" {
		parts = append(parts, hlsPart{URL: manifest.Init})
	}
	for _, segment := range manifest.Segments {
		parts = append(parts, hlsPart{
			URL: segment.URL, Key: segment.Key, Sequence: segment.Sequence, HasSequence: true,
		})
	}
	proofs := []hlsProof{}
	for index, part := range parts {
		if err := contextErr(input.Ctx); err != nil {
			return HlsTrack{}, intermediates, err
		}
		name := fmt.Sprintf("%s-hls-%d-%d", input.Key, trackIndex, index)
		identity := sha256Hex(input.Identity + part.URL)
		partResult, err := TransferMedia(TransferInput{
			Directory: input.Directory,
			Key:       name,
			Identity:  identity,
			URL:       part.URL,
			Headers:   input.Headers,
			Fetch:     input.Fetch,
			Ctx:       input.Ctx,
			MaxBytes:  64 * 1024 * 1024,
			Progress: func(bytes int64, _ *int64) {
				if input.Progress != nil {
					input.Progress(*downloaded+bytes, nil)
				}
			},
		})
		if err != nil {
			return HlsTrack{}, intermediates, err
		}
		*downloaded += partResult.Size
		intermediates = append(intermediates, name+".partial", name+".resume.json", name+".resume.json.writing")
		proofs = append(proofs, hlsProof{
			Path: partResult.Path, Hash: partResult.Hash,
			Key: part.Key, Sequence: part.Sequence, HasSequence: part.HasSequence,
		})
	}
	name := fmt.Sprintf("%s-hls-%d.media", input.Key, trackIndex)
	artifact, err := NewOwnedMediaArtifact(input.Directory, name, hlsPlan(proofs))
	if err != nil {
		return HlsTrack{}, intermediates, err
	}
	reused, err := artifact.Prepare()
	if err != nil {
		return HlsTrack{}, intermediates, err
	}
	if !reused {
		if err := writeHlsArtifact(artifact, proofs, keys, input); err != nil {
			return HlsTrack{}, intermediates, err
		}
	}
	if _, err := artifact.Finish(); err != nil {
		return HlsTrack{}, intermediates, err
	}
	intermediates = append(intermediates, name)
	intermediates = append(intermediates, artifact.Intermediates()...)
	return HlsTrack{Path: artifact.Path, Kind: track.Kind}, intermediates, nil
}

func DownloadNativeHls(input HlsInput) (*HlsResult, error) {
	if !hlsKeyPattern.MatchString(input.Key) {
		return nil, fmt.Errorf("HLS 任务标识无效")
	}
	check := input.Check
	if check == nil {
		check = func(value string) (string, error) { return value, nil }
	}
	intermediates := []string{}
	keys := map[string][]byte{}
	defer func() {
		for _, value := range keys {
			for index := range value {
				value[index] = 0
			}
		}
	}()
	checkedRoot, err := check(input.URL)
	if err != nil {
		return nil, err
	}
	selected, err := resolveHlsTracks(input, check, checkedRoot, 0)
	if err != nil {
		return nil, err
	}
	downloaded := int64(0)
	result := &HlsResult{Tracks: []HlsTrack{}, Intermediates: intermediates}
	for trackIndex, track := range selected {
		if err := contextErr(input.Ctx); err != nil {
			return nil, err
		}
		value, list, err := downloadHlsTrack(input, check, keys, track, trackIndex, &downloaded, intermediates)
		if err != nil {
			return nil, err
		}
		intermediates = list
		result.Intermediates = intermediates
		result.Tracks = append(result.Tracks, value)
	}
	return result, nil
}

func contextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return contract.NewError(contract.CodeCancelled, "下载已取消")
	default:
		return nil
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hlsPlan(proofs []hlsProof) string {
	tuples := make([]any, 0, len(proofs))
	for _, proof := range proofs {
		var keyValue any
		if proof.Key != nil {
			keyValue = map[string]any{"url": proof.Key.URL, "iv": proof.Key.IV}
		}
		var sequence any
		if proof.HasSequence {
			sequence = strconv.FormatUint(proof.Sequence, 10)
		}
		tuples = append(tuples, []any{proof.Hash, keyValue, sequence})
	}
	encoded, _ := json.Marshal([]any{"hls-v1", tuples})
	return sha256Hex(string(encoded))
}

func writeHlsPart(output *os.File, reader io.Reader, verifyHash string, size *int64) error {
	hash := sha256.New()
	buffer := make([]byte, 65536)
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 {
			hash.Write(buffer[:read])
			if _, err := output.Write(buffer[:read]); err != nil {
				return fmt.Errorf("HLS 合并写入中断")
			}
			*size += int64(read)
			if *size > 50*1024*1024*1024 {
				return fmt.Errorf("HLS 媒体超过任务大小上限")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("HLS 合并写入中断")
		}
	}
	if verifyHash != "" && hex.EncodeToString(hash.Sum(nil)) != verifyHash {
		return fmt.Errorf("HLS 恢复片段已改变")
	}
	return nil
}

func hlsKeyBytes(input HlsInput, proof hlsProof, keys map[string][]byte) ([]byte, error) {
	if cached, ok := keys[proof.Key.URL]; ok {
		return cached, nil
	}
	if len(keys) >= 100 {
		return nil, fmt.Errorf("HLS 密钥轮换超出预算")
	}
	response, err := input.Fetch(proof.Key.URL, input.Headers, input.Ctx)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 17))
	_ = response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("HLS 密钥读取失败")
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("HLS 密钥长度无效")
	}
	keys[proof.Key.URL] = raw
	return raw, nil
}

func decryptHlsProof(proof hlsProof, keyBytes []byte) ([]byte, error) {
	raw, err := os.ReadFile(proof.Path)
	if err != nil {
		return nil, fmt.Errorf("HLS 恢复片段已改变")
	}
	if sha256Hex(string(raw)) != proof.Hash {
		return nil, fmt.Errorf("HLS 恢复片段已改变")
	}
	if len(raw)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("HLS 恢复片段长度无效")
	}
	iv := make([]byte, aes.BlockSize)
	if proof.Key.IV != "" {
		decoded, err := hex.DecodeString(strings.TrimPrefix(proof.Key.IV, "0x"))
		if err != nil || len(decoded) != aes.BlockSize {
			return nil, fmt.Errorf("此 HLS 加密格式尚不支持")
		}
		copy(iv, decoded)
	} else {
		binary.BigEndian.PutUint64(iv[8:], proof.Sequence)
	}
	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("HLS 密钥无效")
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw) // NOSONAR: HLS AES-128 mandates AES-CBC + PKCS#7; not a selectable cipher mode.
	return pkcs7Unpad(plain)
}

func writeHlsArtifact(artifact *OwnedMediaArtifact, proofs []hlsProof, keys map[string][]byte, input HlsInput) error {
	output, err := os.OpenFile(artifact.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("HLS 合并写入失败")
	}
	defer output.Close()
	size := int64(0)
	for _, proof := range proofs {
		if err := contextErr(input.Ctx); err != nil {
			return err
		}
		if proof.Key != nil {
			keyBytes, err := hlsKeyBytes(input, proof, keys)
			if err != nil {
				return err
			}
			plain, err := decryptHlsProof(proof, keyBytes)
			if err != nil {
				return err
			}
			if err := writeHlsPart(output, bytes.NewReader(plain), "", &size); err != nil {
				return err
			}
			continue
		}
		file, err := os.Open(proof.Path)
		if err != nil {
			return fmt.Errorf("HLS 恢复片段已改变")
		}
		err = writeHlsPart(output, file, proof.Hash, &size)
		_ = file.Close()
		if err != nil {
			return err
		}
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("HLS 合并写入中断")
	}
	return nil
}

func pkcs7Unpad(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("HLS 恢复片段填充无效")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(value) {
		return nil, fmt.Errorf("HLS 恢复片段填充无效")
	}
	for _, byteValue := range value[len(value)-padding:] {
		if int(byteValue) != padding {
			return nil, fmt.Errorf("HLS 恢复片段填充无效")
		}
	}
	return value[:len(value)-padding], nil
}
