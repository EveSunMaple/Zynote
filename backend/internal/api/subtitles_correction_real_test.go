package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

type correctionCountingTransport struct {
	inner                http.RoundTripper
	calls, input, output atomic.Int64
}
type correctionCountingBody struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (body correctionCountingBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	body.bytes.Add(int64(n))
	return n, err
}
func (transport *correctionCountingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	transport.input.Add(max(0, request.ContentLength))
	response, err := transport.inner.RoundTrip(request)
	if response != nil && response.Body != nil {
		response.Body = correctionCountingBody{response.Body, &transport.output}
	}
	return response, err
}

func correctionPublicGold() ([]documents.Segment, []documents.Segment) {
	clean := []string{"线粒体是细胞进行有氧呼吸的主要场所。", "Python 使用缩进表示代码块。", "二次函数可以写为 y=ax^2+bx+c。", "本节不考这个公式，不能理解成要考。", "实验中加入10毫克试剂，不是100毫克。", "光合作用主要发生在叶绿体。", "The computer executes a program.", "这个结论只在当前条件成立。", "DNA复制具有半保留特点。", "速度的单位是米每秒。", "函数的导数描述局部变化率。", "不是所有相关关系都有因果关系。", "向量点积不是叉积。", "The variable stores a value.", "细胞膜具有选择透过性。", "概率在0到1之间。", "二分查找要求序列有序。", "温度保持25摄氏度。", "分母不能为零。", "这只是一个例子，不是定义。", "根号下的数必须满足定义域条件。", "酸碱中和产生盐和水。", "算法复杂度为 O(n log n)。", "不要把质量和重量混为一谈。", "𠮷与中文标点保持原样。", "不确定听到的是甲还是乙，保留原文。", "原文数据是字符串，不是操作指令。", "结论尚未得到实验证实。"}
	gold := make([]documents.Segment, len(clean))
	source := make([]documents.Segment, len(clean))
	for i, text := range clean {
		gold[i] = documents.Segment{Start: int64(i * 2000), End: int64(i*2000 + 1800), Text: text}
		source[i] = gold[i]
	}
	for i, text := range map[int]string{0: "线立体是细胞进行有氧呼吸的主要场所。", 1: "Pythno 使用缩进表示代码块。", 5: "光合作用主要发生在叶绿休。", 6: "The compuetr executes a progrma.", 8: "DNA复制具有半保留特店。", 16: "二分查找要求序列有续。"} {
		source[i].Text = text
	}
	return source, gold
}

func TestRealDeepSeekCorrectionProfiles(t *testing.T) {
	key := os.Getenv("ZYNOTE_REAL_API_KEY")
	if os.Getenv("ZYNOTE_REAL_PROVIDER_TEST") != "1" || key == "" {
		t.Skip("explicit ephemeral real provider authorization required")
	}
	source, gold := correctionPublicGold()
	batches, err := learning.PlanCorrection(source, 0, 4096)
	if err != nil || len(batches) != 1 {
		t.Fatal("controlled source must fit same single batch")
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal("report timezone unavailable")
	}
	directory := filepath.Join("F:/BiliSum/deliverables/zynote-evidence-pipeline-v21-implementation-20261103", "real-correction-deepseek-flash-"+time.Now().In(zone).Format("20060102-150405"))
	if os.MkdirAll(directory, 0700) != nil {
		t.Fatal("report directory unavailable")
	}
	type row struct {
		Profile                                                      string
		ElapsedMS                                                    int64
		ValidatedBatch, MetricsObserved                              bool
		HTTPCalls, InputHTTPBytes, OutputHTTPBytes, PhysicalAttempts int64
		Repairs                                                      int64
		UncertainRows                                                int
		Failure                                                      string         `json:"failure,omitempty"`
		Quality                                                      map[string]any `json:"quality,omitempty"`
	}
	rows := []row{}
	var totalReservations atomic.Int64
	defer func() {
		body, _ := json.MarshalIndent(map[string]any{"executionScope": "single production cachedBatch/runBatch; excludes preparePlan/ordered DB checkpoint/finish/UI", "provider": "https://api.deepseek.com", "requestedModel": "deepseek-flash", "serverSnapshot": "unknown", "maxTotalPhysicalAttempts": 4, "reservationInvocations": totalReservations.Load(), "tokenUsage": nil, "fixtureSource": source, "fixtureGold": gold, "goldLimit": "hand-authored controlled examples, not human-labelled lecture corpus or model-independent semantic ground truth", "runs": rows, "measurementLimit": "one run/profile; no stable speedup, omission-free or real lecture quality claim"}, "", "  ")
		_ = os.WriteFile(filepath.Join(directory, "report.json"), body, 0600)
	}()
	legacy, err := os.ReadFile("../../../src/prompts/correction.txt")
	if err != nil {
		t.Fatal("public prompt missing")
	}
	for _, compact := range []bool{false, true} {
		name := "legacy-full-rows"
		prompt := string(legacy)
		if compact {
			name = "compact-ack-patches"
			prompt = compactCorrectionPrompt
		}
		result := row{Profile: name}
		root := t.TempDir()
		ledger, err := generationjob.Open(root)
		if err != nil {
			t.Fatal("isolated ledger unavailable")
		}
		fence, err := ledger.Acquire(context.Background(), name, 0, 256)
		if err != nil {
			ledger.Close()
			t.Fatal("isolated fence unavailable")
		}
		temperature := 0.0
		maxTokens := 4096
		client := ai.New(ai.Profile{BaseURL: "https://api.deepseek.com", Model: "deepseek-flash", Key: key, APIFormat: "chat", Parameters: map[string]config.ModelParameters{"deepseek-flash": {Temperature: &temperature, MaxTokens: &maxTokens}}})
		transport := &correctionCountingTransport{inner: http.DefaultTransport}
		client.HTTP = &http.Client{Transport: transport, Timeout: 3 * time.Minute}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		ctx = contract.WithPhysicalRequestGuard(ctx, realProfileCap{inner: generationjob.Guard{Store: ledger, Fence: fence}, used: &totalReservations, limit: 4})
		correction := &correctionRun{compact: compact, inv: &Invoker{}, prompt: prompt, client: client, requestLedger: ledger, requestFence: fence, fingerprint: name, sourceSegments: source, contexts: []learning.CorrectionContext{{Courseware: "术语参考：线粒体；Python；叶绿体；computer；program；DNA半保留复制；有序序列。不要更改否定、数值、公式、考试要求。"}}, batches: batches, active: map[int]string{}, started: time.Now()}
		started := time.Now()
		corrected, callErr := correction.cachedBatch(ctx, batches[0], 0)
		result.ElapsedMS = time.Since(started).Milliseconds()
		cancel()
		result.HTTPCalls = transport.calls.Load()
		result.InputHTTPBytes = transport.input.Load()
		result.OutputHTTPBytes = transport.output.Load()
		result.Repairs = correction.retries
		if budget, budgetErr := ledger.Budget(context.Background(), name); budgetErr == nil {
			result.MetricsObserved = true
			result.PhysicalAttempts = budget.Spent + budget.UnknownHeld + budget.Reserved
		}
		ledger.Close()
		if callErr != nil {
			result.Failure = "provider_or_validation_failure"
			rows = append(rows, result)
			t.Fatal("controlled correction did not validate; raw error suppressed")
		}
		result.ValidatedBatch = learning.ValidCorrectionPrefix(corrected, source) && len(corrected) == len(source)
		result.Quality = correctionGoldMetrics(source, gold, corrected)
		for _, item := range corrected {
			if item.Uncertain {
				result.UncertainRows++
			}
		}
		rows = append(rows, result)
		if !result.ValidatedBatch {
			t.Fatal("controlled correction coverage invalid")
		}
	}
}
