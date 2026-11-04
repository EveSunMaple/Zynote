package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
)

const compactCorrectionPrompt = "你正在校订智云课堂的语音识别原文。结合PPT及相邻上下文逐条审阅rows，只修正有依据的错别字、术语、明显漏词和断句，保留教师口语及全部原始信息。课件不是完整讲稿，不得补写知识、例子、考试要求或时间；不得改变否定、数值、公式含义与考试语气。lectureContext只读，不参与输出。仅返回JSON对象{processing:\"complete\",processed:[按原顺序逐一列出全部输入row的整数id],patches:[{id:整数,text:校订文字,uncertain:布尔,reason:简短依据}]}。必须审阅所有rows并完整确认processed，不能因没有改动省略处理确认。patches只列有依据的改动或疑点，未改且无疑点不重复原文。每个id最多一条patch；修改必须有简短依据，不能编造。不确定时uncertain=true且text保留原文，reason说明候选解释；不能把猜测写成确定纠正。全部已审阅但无需修改时patches返回空数组。"
const compactCorrectionInstructions = "逐条审阅全部rows，完整同序列出processed；patches只返回有依据的改动或疑点。相邻原文只读不返回。疑点保留原文，严禁猜改否定、数值、公式和考试语气。"
const canonicalCorrectionPromptSHA256 = "e84c3601c13100981f7ab146f9ed2917522fdc8534f989849b0ff6333ba7f890"

func canonicalCorrectionPrompt(prompt string) bool {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ReplaceAll(prompt, "\r\n", "\n"))))
	return hex.EncodeToString(sum[:]) == canonicalCorrectionPromptSHA256
}

// A historical job is authoritative even without a checkpoint: changing wire
// format must never escape its held UNKNOWN liability or replay paid work.
func freshCompactCorrection(ctx context.Context, root, lectureID, legacyFingerprint, checkpointFingerprint, prompt string, compactFingerprint ...string) (bool, error) {
	if !canonicalCorrectionPrompt(prompt) {
		return false, nil
	}
	ledger, err := generationjob.Open(root)
	if err != nil {
		return false, err
	}
	defer ledger.Close()
	generation, err := ledger.CurrentGeneration(ctx, "correction-"+lectureID+"-"+legacyFingerprint)
	if err != nil {
		return false, err
	}
	compactGeneration := int64(0)
	if len(compactFingerprint) > 0 {
		compactGeneration, err = ledger.CurrentGeneration(ctx, "correction-"+lectureID+"-"+compactFingerprint[0])
		if err != nil {
			return false, err
		}
	}
	if compactGeneration > 0 && (generation > 0 || checkpointFingerprint == legacyFingerprint) {
		return false, fmt.Errorf("同一原文存在两种校订协议历史，未自动切换或重放，请先核对任务记录")
	}
	return compactGeneration > 0 || (generation == 0 && checkpointFingerprint != legacyFingerprint), nil
}
