package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

var discussionRefPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (inv *Invoker) handleMediaDiscussionImport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Ref      string          `json:"ref"`
		Changes  json.RawMessage `json:"changes"`
		Category string          `json:"category"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !discussionRefPattern.MatchString(input.Ref) {
		return nil, contract.InputInvalid("讨论记录编号无效")
	}
	category := strings.TrimSpace(input.Category)
	if category == "" {
		category = "观众讨论"
	}
	if len([]rune(category)) > 100 {
		return nil, contract.InputInvalid("资料分类无效")
	}
	changes, err := bilibili.ParseDiscussionChanges(input.Changes)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	mediaService, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	discussionStore, err := bilibili.NewDiscussionStore(mediaService.Root)
	if err != nil {
		return nil, err
	}
	pack, text, err := discussionStore.Select(input.Ref, changes)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	knowledgeStore, err := inv.knowledgeStoreFor(state)
	if err != nil {
		return nil, err
	}
	locator := fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d#discussion", pack.Bvid, pack.Page)
	title := fmt.Sprintf("%s · P%d · 评论与弹幕", pack.Bvid, pack.Page)
	expected, err := knowledgeStore.Generation(locator)
	if err != nil {
		return nil, err
	}
	expectedRaw, _ := json.Marshal(expected)
	expectedJSON := json.RawMessage(expectedRaw)
	sourceMethod := "观众讨论（未核实）"
	receipt, err := knowledgeStore.Import(knowledge.ImportInput{
		Title: title, Text: text, Locator: locator, SourceHash: sha256Hex(text), Category: category,
		Origin: "bilibili-discussion", SourceMethod: &sourceMethod, ExpectedGeneration: &expectedJSON,
	})
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Notes", category)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, library.FolderName(title)+"-"+input.Ref[:12]+".md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, contract.Unavailable("讨论保存失败")
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != text {
		return nil, contract.Unavailable("讨论保存核验失败")
	}
	return map[string]any{"receipt": receipt, "path": path}, nil
}
