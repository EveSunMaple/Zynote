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

func ParseHls(text, base string, check func(string) (string, error)) (HlsManifest, error) {
	manifest := HlsManifest{Variants: []HlsVariant{}, Audio: []HlsAudio{}, Segments: []HlsSegment{}}
	normalized := strings.TrimPrefix(text, "\uFEFF")
	rawLines := strings.Split(strings.ReplaceAll(normalized, "\r\n", "\n"), "\n")
	lines := []string{}
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 || lines[0] != "#EXTM3U" || len(lines) > 50000 {
		return manifest, fmt.Errorf("HLS 清单无效或过大")
	}
	resolve := func(raw string) (string, error) {
		resolved, err := hlsResolve(base, raw)
		if err != nil {
			return "", err
		}
		return check(resolved)
	}
	var pending map[string]string
	var duration *float64
	var sequence uint64
	var key *HlsKey
	var initURL string
	ended := false
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs, err := hlsAttrs(line)
			if err != nil {
				return manifest, err
			}
			pending = attrs
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			attrs, err := hlsAttrs(line)
			if err != nil {
				return manifest, err
			}
			if attrs["TYPE"] == "AUDIO" && attrs["URI"] != "" {
				resolved, err := resolve(attrs["URI"])
				if err != nil {
					return manifest, err
				}
				manifest.Audio = append(manifest.Audio, HlsAudio{
					URL: resolved, Group: attrs["GROUP-ID"], Preferred: attrs["DEFAULT"] == "YES",
				})
			}
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			value := strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")
			if !hlsSequencePattern.MatchString(value) {
				return manifest, fmt.Errorf("HLS 序号无效")
			}
			sequence, _ = strconv.ParseUint(value, 10, 64)
		case strings.HasPrefix(line, "#EXTINF:"):
			raw := strings.SplitN(strings.TrimPrefix(line, "#EXTINF:"), ",", 2)[0]
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || parsed <= 0 || parsed > 3600 {
				return manifest, fmt.Errorf("HLS 片段时间无效")
			}
			duration = &parsed
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs, err := hlsAttrs(line)
			if err != nil {
				return manifest, err
			}
			if attrs["METHOD"] == "NONE" {
				key = nil
			} else {
				if attrs["METHOD"] != "AES-128" || attrs["URI"] == "" ||
					(attrs["KEYFORMAT"] != "" && attrs["KEYFORMAT"] != "identity") ||
					(attrs["IV"] != "" && !hlsIVPattern.MatchString(attrs["IV"])) {
					return manifest, fmt.Errorf("此 HLS 加密格式尚不支持")
				}
				resolved, err := resolve(attrs["URI"])
				if err != nil {
					return manifest, err
				}
				key = &HlsKey{URL: resolved, IV: attrs["IV"]}
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			attrs, err := hlsAttrs(line)
			if err != nil {
				return manifest, err
			}
			if attrs["URI"] == "" || attrs["BYTERANGE"] != "" || key != nil {
				return manifest, fmt.Errorf("此 HLS 初始化范围或加密格式尚不支持")
			}
			next, err := resolve(attrs["URI"])
			if err != nil {
				return manifest, err
			}
			if initURL != "" && initURL != next {
				return manifest, fmt.Errorf("HLS 初始化轨道改变，请使用独立片段任务")
			}
			initURL = next
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:") || strings.HasPrefix(line, "#EXT-X-GAP"):
			return manifest, fmt.Errorf("此 HLS 字节范围或缺失片段尚不支持")
		case line == "#EXT-X-ENDLIST":
			ended = true
		case !strings.HasPrefix(line, "#"):
			if pending != nil {
				height := 0
				if resolution := pending["RESOLUTION"]; resolution != "" {
					parts := strings.Split(resolution, "x")
					if len(parts) == 2 {
						parsed, err := strconv.Atoi(parts[1])
						if err == nil {
							height = parsed
						} else {
							return manifest, fmt.Errorf("HLS 格式目录无效")
						}
					}
				}
				bandwidth := 0.0
				if raw := pending["BANDWIDTH"]; raw != "" {
					parsed, err := strconv.ParseFloat(raw, 64)
					if err != nil || math.IsNaN(parsed) || parsed < 0 {
						return manifest, fmt.Errorf("HLS 格式目录无效")
					}
					bandwidth = parsed
				}
				if height < 0 {
					return manifest, fmt.Errorf("HLS 格式目录无效")
				}
				resolved, err := resolve(line)
				if err != nil {
					return manifest, err
				}
				manifest.Variants = append(manifest.Variants, HlsVariant{
					URL: resolved, Height: height, Bandwidth: bandwidth,
					Codec: pending["CODECS"], Audio: pending["AUDIO"],
				})
				pending = nil
			} else {
				if duration == nil || sequence == math.MaxUint64 {
					return manifest, fmt.Errorf("HLS 片段缺少时间或序号")
				}
				resolved, err := resolve(line)
				if err != nil {
					return manifest, err
				}
				manifest.Segments = append(manifest.Segments, HlsSegment{
					URL: resolved, Duration: *duration, Sequence: sequence, Key: key,
				})
				sequence++
				duration = nil
				if len(manifest.Segments) > 10000 {
					return manifest, fmt.Errorf("HLS 片段超过本次有界任务上限")
				}
			}
		}
	}
	if pending != nil || duration != nil || (len(manifest.Variants) > 0 && len(manifest.Segments) > 0) ||
		len(manifest.Variants) > 200 {
		return manifest, fmt.Errorf("HLS 清单结构不完整")
	}
	if len(manifest.Variants) == 0 && (!ended || len(manifest.Segments) == 0) {
		return manifest, fmt.Errorf("仅支持已结束且完整的 HLS 录像，未把直播窗口当作完整视频")
	}
	manifest.Init = initURL
	return manifest, nil
}

type HlsInput struct {
	Directory string
	Key       string
	Identity  string
	URL       string
	Options   NativeMediaOptions
	Headers   map[string]string
	Ctx       context.Context
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
	read := func(requestURL string) (HlsManifest, error) {
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
	var resolve func(requestURL string, depth int) ([]hlsTrackRef, error)
	resolve = func(requestURL string, depth int) ([]hlsTrackRef, error) {
		if depth > 2 {
			return nil, fmt.Errorf("HLS 目录嵌套过多")
		}
		manifest, err := read(requestURL)
		if err != nil {
			return nil, err
		}
		if len(manifest.Variants) == 0 {
			return []hlsTrackRef{{URL: requestURL, Kind: "combined"}}, nil
		}
		prefix := ""
		switch input.Options.VideoCodec {
		case "h264":
			prefix = "avc"
		case "h265":
			prefix = "hev"
		case "vp9":
			prefix = "vp"
		case "av1":
			prefix = "av01"
		}
		candidates := []HlsVariant{}
		for _, variant := range manifest.Variants {
			if input.Options.Height != 0 && (variant.Height <= 0 || variant.Height > input.Options.Height) {
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
		picked := candidates[0]
		var audio *HlsAudio
		for index := range manifest.Audio {
			if manifest.Audio[index].Group != picked.Audio {
				continue
			}
			if audio == nil || (manifest.Audio[index].Preferred && !audio.Preferred) {
				audio = &manifest.Audio[index]
			}
		}
		paths, err := resolve(picked.URL, depth+1)
		if err != nil {
			return nil, err
		}
		if audio != nil {
			if len(paths) != 1 {
				return nil, fmt.Errorf("HLS 音轨嵌套有歧义")
			}
			return []hlsTrackRef{
				{URL: paths[0].URL, Kind: "video"},
				{URL: audio.URL, Kind: "audio"},
			}, nil
		}
		return paths, nil
	}
	checkedRoot, err := check(input.URL)
	if err != nil {
		return nil, err
	}
	selected, err := resolve(checkedRoot, 0)
	if err != nil {
		return nil, err
	}
	downloaded := int64(0)
	result := &HlsResult{Tracks: []HlsTrack{}, Intermediates: intermediates}
	for trackIndex, track := range selected {
		if err := contextErr(input.Ctx); err != nil {
			return nil, err
		}
		manifest, err := read(track.URL)
		if err != nil {
			return nil, err
		}
		if len(manifest.Variants) > 0 {
			return nil, fmt.Errorf("HLS 音轨需要具体媒体清单")
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
				return nil, err
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
						input.Progress(downloaded+bytes, nil)
					}
				},
			})
			if err != nil {
				return nil, err
			}
			downloaded += partResult.Size
			intermediates = append(intermediates, name+".partial", name+".resume.json", name+".resume.json.writing")
			proofs = append(proofs, hlsProof{
				Path: partResult.Path, Hash: partResult.Hash,
				Key: part.Key, Sequence: part.Sequence, HasSequence: part.HasSequence,
			})
		}
		name := fmt.Sprintf("%s-hls-%d.media", input.Key, trackIndex)
		plan := hlsPlan(proofs)
		artifact, err := NewOwnedMediaArtifact(input.Directory, name, plan)
		if err != nil {
			return nil, err
		}
		reused, err := artifact.Prepare()
		if err != nil {
			return nil, err
		}
		if !reused {
			if err := writeHlsArtifact(artifact, proofs, keys, input); err != nil {
				return nil, err
			}
		}
		if _, err := artifact.Finish(); err != nil {
			return nil, err
		}
		intermediates = append(intermediates, name)
		intermediates = append(intermediates, artifact.Intermediates()...)
		result.Intermediates = intermediates
		result.Tracks = append(result.Tracks, HlsTrack{Path: artifact.Path, Kind: track.Kind})
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

func writeHlsArtifact(artifact *OwnedMediaArtifact, proofs []hlsProof, keys map[string][]byte, input HlsInput) error {
	output, err := os.OpenFile(artifact.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("HLS 合并写入失败")
	}
	defer output.Close()
	size := int64(0)
	writePart := func(reader io.Reader, verifyHash string) error {
		hash := sha256.New()
		buffer := make([]byte, 65536)
		for {
			read, readErr := reader.Read(buffer)
			if read > 0 {
				hash.Write(buffer[:read])
				if _, err := output.Write(buffer[:read]); err != nil {
					return fmt.Errorf("HLS 合并写入中断")
				}
				size += int64(read)
				if size > 50*1024*1024*1024 {
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
	for _, proof := range proofs {
		if err := contextErr(input.Ctx); err != nil {
			return err
		}
		if proof.Key != nil {
			keyBytes, ok := keys[proof.Key.URL]
			if !ok {
				if len(keys) >= 100 {
					return fmt.Errorf("HLS 密钥轮换超出预算")
				}
				response, err := input.Fetch(proof.Key.URL, input.Headers, input.Ctx)
				if err != nil {
					return err
				}
				raw, err := io.ReadAll(io.LimitReader(response.Body, 17))
				_ = response.Body.Close()
				if err != nil {
					return fmt.Errorf("HLS 密钥读取失败")
				}
				if len(raw) != 16 {
					return fmt.Errorf("HLS 密钥长度无效")
				}
				keys[proof.Key.URL] = raw
				keyBytes = raw
			}
			raw, err := os.ReadFile(proof.Path)
			if err != nil {
				return fmt.Errorf("HLS 恢复片段已改变")
			}
			if sha256Hex(string(raw)) != proof.Hash {
				return fmt.Errorf("HLS 恢复片段已改变")
			}
			if len(raw)%aes.BlockSize != 0 {
				return fmt.Errorf("HLS 恢复片段长度无效")
			}
			iv := make([]byte, aes.BlockSize)
			if proof.Key.IV != "" {
				decoded, err := hex.DecodeString(strings.TrimPrefix(proof.Key.IV, "0x"))
				if err != nil || len(decoded) != aes.BlockSize {
					return fmt.Errorf("此 HLS 加密格式尚不支持")
				}
				copy(iv, decoded)
			} else {
				binary.BigEndian.PutUint64(iv[8:], proof.Sequence)
			}
			block, err := aes.NewCipher(keyBytes)
			if err != nil {
				return fmt.Errorf("HLS 密钥无效")
			}
			plain := make([]byte, len(raw))
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
			plain, err = pkcs7Unpad(plain)
			if err != nil {
				return err
			}
			if err := writePart(bytes.NewReader(plain), ""); err != nil {
				return err
			}
		} else {
			file, err := os.Open(proof.Path)
			if err != nil {
				return fmt.Errorf("HLS 恢复片段已改变")
			}
			err = writePart(file, proof.Hash)
			_ = file.Close()
			if err != nil {
				return err
			}
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
