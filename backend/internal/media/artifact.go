package media

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const (
	artifactWritingSuffix    = ".writing"
	artifactIncompletePrefix = ".incomplete-"
)

func MediaFileHash(path string) (string, error) {
	hash, _, err := hashFileStreaming(path)
	return hash, err
}

var artifactNameForbidden = regexp.MustCompile(`[/\\<>:"|?*\x00-\x1f]`)
var incompletePattern = regexp.MustCompile(`^\.incomplete-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-.+$`)

type artifactReservation struct {
	Version int      `json:"version"`
	Owner   string   `json:"owner"`
	Name    string   `json:"name"`
	Plan    string   `json:"plan"`
	Prior   []string `json:"prior"`
}

type artifactReceipt struct {
	Version int    `json:"version"`
	Owner   string `json:"owner"`
	Plan    string `json:"plan"`
	Bytes   int64  `json:"bytes"`
	Hash    string `json:"hash"`
}

type OwnedMediaArtifact struct {
	directory string
	name      string
	plan      string
	Path      string
	reserve   string
	proof     string
	owner     *artifactReservation
}

func NewOwnedMediaArtifact(directory, name, plan string) (*OwnedMediaArtifact, error) {
	if name == "" || artifactNameForbidden.MatchString(name) || strings.Contains(name, "..") ||
		reservedRecordNames[strings.ToLower(name)] {
		return nil, fmt.Errorf("媒体成果文件名无效")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(plan) {
		return nil, fmt.Errorf("媒体成果计划无效")
	}
	path := filepath.Join(directory, name)
	return &OwnedMediaArtifact{
		directory: directory, name: name, plan: plan, Path: path,
		reserve: path + ".reservation.json", proof: path + ".receipt.json",
	}, nil
}

func artifactJSON(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("媒体成果记录不是普通文件")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("媒体成果记录不是普通文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("媒体成果记录缺失或无效")
	}
	if info.Size() > 256000 {
		return nil, nil, fmt.Errorf("媒体成果记录缺失或无效")
	}
	return raw, info, nil
}

func artifactAtomic(path string, value any) error {
	for _, target := range []string{path, path + artifactWritingSuffix} {
		if info, err := os.Lstat(target); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("媒体成果记录不是普通文件")
		}
	}
	encoded, _ := json.Marshal(value)
	if err := os.WriteFile(path+artifactWritingSuffix, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(path+artifactWritingSuffix, path)
}

func parseArtifactOwner(raw []byte, artifact *OwnedMediaArtifact) (*artifactReservation, error) {
	if raw == nil {
		return nil, nil
	}
	owner := &artifactReservation{}
	if json.Unmarshal(raw, owner) != nil {
		return nil, fmt.Errorf("已有媒体恢复记录无效，原文件保留")
	}
	if owner.Name != artifact.name || owner.Plan != artifact.plan {
		return nil, fmt.Errorf("已有媒体恢复记录与本次设置不同，原文件保留")
	}
	for _, name := range owner.Prior {
		stem := strings.TrimPrefix(name, artifactIncompletePrefix)
		stem = strings.TrimSuffix(stem, "-"+artifact.name)
		if !incompletePattern.MatchString(name) ||
			!regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(stem) {
			return nil, fmt.Errorf("已有媒体恢复记录与本次设置不同，原文件保留")
		}
	}
	return owner, nil
}

func (artifact *OwnedMediaArtifact) archiveOutput(owner *artifactReservation, fileInfo os.FileInfo) error {
	if fileInfo == nil {
		if owner == nil {
			return nil
		}
		archived := artifactIncompletePrefix + owner.Owner + "-" + artifact.name
		if _, err := os.Lstat(filepath.Join(artifact.directory, archived)); err == nil {
			owner.Owner = ids.New()
			owner.Prior = append(owner.Prior, archived)
			return artifactAtomic(artifact.reserve, owner)
		}
		return nil
	}
	if owner == nil {
		return fmt.Errorf("媒体输出已经存在，未覆盖原文件")
	}
	archived := artifactIncompletePrefix + owner.Owner + "-" + artifact.name
	if _, err := os.Lstat(filepath.Join(artifact.directory, archived)); err == nil {
		return fmt.Errorf("媒体恢复备份已存在，保留当前文件")
	}
	if err := os.Rename(artifact.Path, filepath.Join(artifact.directory, archived)); err != nil {
		return err
	}
	owner.Owner = ids.New()
	owner.Prior = append(owner.Prior, archived)
	return artifactAtomic(artifact.reserve, owner)
}

func artifactProofMatches(proof artifactReceipt, owner *artifactReservation, fileInfo os.FileInfo, artifact *OwnedMediaArtifact) bool {
	if owner == nil || fileInfo == nil || proof.Owner != owner.Owner || proof.Plan != artifact.plan ||
		proof.Bytes != fileInfo.Size() {
		return false
	}
	fileHash, _ := MediaFileHash(artifact.Path)
	return proof.Hash == fileHash
}

func (artifact *OwnedMediaArtifact) Prepare() (bool, error) {
	_, fileInfo, err := artifactJSON(artifact.Path)
	if err != nil {
		return false, err
	}
	reservedRaw, _, err := artifactJSON(artifact.reserve)
	if err != nil {
		return false, err
	}
	proofRaw, _, err := artifactJSON(artifact.proof)
	if err != nil {
		return false, err
	}
	owner, err := parseArtifactOwner(reservedRaw, artifact)
	if err != nil {
		return false, err
	}
	if proofRaw != nil {
		proof := artifactReceipt{}
		if json.Unmarshal(proofRaw, &proof) != nil {
			return false, fmt.Errorf("媒体核验记录无效，原文件保留")
		}
		if !artifactProofMatches(proof, owner, fileInfo, artifact) {
			return false, fmt.Errorf("已有待发布成果被修改或缺失，原文件保留")
		}
		artifact.owner = owner
		return true, nil
	}
	if err := artifact.archiveOutput(owner, fileInfo); err != nil {
		return false, err
	}
	if owner == nil {
		owner = &artifactReservation{Version: 1, Owner: ids.New(), Name: artifact.name, Plan: artifact.plan, Prior: []string{}}
		if err := artifactAtomic(artifact.reserve, owner); err != nil {
			return false, err
		}
	}
	artifact.owner = owner
	return false, nil
}

func (artifact *OwnedMediaArtifact) Finish() (artifactReceipt, error) {
	if artifact.owner == nil {
		return artifactReceipt{}, fmt.Errorf("成果尚未预留")
	}
	info, err := os.Lstat(artifact.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return artifactReceipt{}, fmt.Errorf("媒体成果为空")
	}
	value := artifactReceipt{
		Version: 1, Owner: artifact.owner.Owner, Plan: artifact.plan, Bytes: info.Size(),
	}
	value.Hash, err = MediaFileHash(artifact.Path)
	if err != nil {
		return artifactReceipt{}, err
	}
	proofRaw, _, err := artifactJSON(artifact.proof)
	if err != nil {
		return artifactReceipt{}, err
	}
	if proofRaw != nil {
		prior := artifactReceipt{}
		if json.Unmarshal(proofRaw, &prior) != nil || prior != value {
			return artifactReceipt{}, fmt.Errorf("媒体核验记录已改变")
		}
	} else if err := artifactAtomic(artifact.proof, value); err != nil {
		return artifactReceipt{}, err
	}
	return value, nil
}

func (artifact *OwnedMediaArtifact) Intermediates() []string {
	if artifact.owner == nil {
		return []string{}
	}
	return append([]string{
		artifact.name + ".reservation.json",
		artifact.name + ".reservation.json.writing",
		artifact.name + ".receipt.json",
		artifact.name + ".receipt.json.writing",
	}, artifact.owner.Prior...)
}
