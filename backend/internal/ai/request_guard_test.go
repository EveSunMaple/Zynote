package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
)

func TestPhysicalGuardIncludesJSONRepairAndHolds429(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ledger, err := generationjob.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			fence, err := ledger.Acquire(context.Background(), "fixture", 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			ctx := contract.WithPhysicalRequestGuard(context.Background(), generationjob.Guard{Store: ledger, Fence: fence})
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"choices":[{"message":{"content":"not-json"}}]}`)
			}))
			defer server.Close()
			client := New(Profile{BaseURL: server.URL, Model: "fixture", APIFormat: "chat_completions"})
			_, err = client.JSON(ctx, "fixture", map[string]string{"input": "fixture"}, func([]byte) error { return fmt.Errorf("fixture requires JSON") })
			if err == nil || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			budget, err := ledger.Budget(context.Background(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if status == http.StatusOK && budget.Spent != 1 {
				t.Fatalf("repair bypassed budget: %+v", budget)
			}
			if status == http.StatusTooManyRequests && budget.UnknownHeld != 1 {
				t.Fatalf("429 released dispatched cost: %+v", budget)
			}
		})
	}
}
