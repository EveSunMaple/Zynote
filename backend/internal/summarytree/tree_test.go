package summarytree

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type memoryStorage map[string][]byte

func (s memoryStorage) Put(_ context.Context, body []byte) (string, error) {
	ref := Digest(body)
	s[ref] = append([]byte(nil), body...)
	return ref, nil
}
func (s memoryStorage) Get(_ context.Context, ref string) ([]byte, error) {
	body, ok := s[ref]
	if !ok {
		return nil, fmt.Errorf("missing")
	}
	return body, nil
}
func TestPagedTreeRecomputesAllLeavesAndRejectsForgedDisposition(t *testing.T) {
	storage := memoryStorage{}
	scope := Scope{SourceSHA256: strings.Repeat("a", 64), RecipeSHA256: strings.Repeat("b", 64), Generation: 1, Epoch: 1}
	builder := New(storage, scope)
	for chunk := 0; chunk < 17; chunk++ {
		points := []contract.ValidatedSummaryPoint{}
		if chunk%3 != 0 {
			points = append(points, contract.ValidatedSummaryPoint{Cue: chunk, Quote: "原话", Point: "结论"})
		}
		if err := builder.Append(context.Background(), chunk, points); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	result, err := builder.Finish(context.Background(), func(leaf Leaf) error { seen++; return nil })
	if err != nil || result.PageCount != 17 || result.LeafCount != 11 || seen != 11 || result.NodeCount <= 17 {
		t.Fatalf("bad tree %+v %v seen=%d", result, err, seen)
	}
	if _, err := Verify(context.Background(), storage, Scope{SourceSHA256: scope.SourceSHA256, RecipeSHA256: scope.RecipeSHA256, Generation: 2, Epoch: 1}, result.Root, nil); err == nil {
		t.Fatal("cross-generation scope accepted")
	}
	var page Node
	for _, body := range storage {
		_ = json.Unmarshal(body, &page)
		if len(page.Leaves) > 0 {
			break
		}
	}
	for _, mutate := range []func(*Node){func(n *Node) { n.Leaves[0].ID = strings.Repeat("f", 64) }, func(n *Node) { n.Leaves[0].Disposition = "omitted" }, func(n *Node) { n.Leaves = append(n.Leaves, n.Leaves[0]) }} {
		raw, _ := json.Marshal(page)
		var candidate Node
		_ = json.Unmarshal(raw, &candidate)
		mutate(&candidate)
		raw, _ = json.Marshal(candidate)
		ref, _ := storage.Put(context.Background(), raw)
		if _, err := Verify(context.Background(), storage, scope, ref, nil); err == nil {
			t.Fatal("forged leaf/disposition accepted")
		}
	}
	root := Node{Version: 1, Scope: scope, Depth: 1, Children: []string{builder.refs[1], builder.refs[1]}}
	raw, _ := json.Marshal(root)
	ref, _ := storage.Put(context.Background(), raw)
	if _, err := Verify(context.Background(), storage, scope, ref, nil); err == nil {
		t.Fatal("duplicate subtree accepted")
	}
}
func TestTreeLimitsDoNotDropTail(t *testing.T) {
	scope := Scope{SourceSHA256: "source", RecipeSHA256: "recipe"}
	storage := memoryStorage{}
	builder := New(storage, scope)
	if err := builder.Append(context.Background(), 1, nil); err == nil {
		t.Fatal("out-of-order commit accepted")
	}
	if err := builder.Append(context.Background(), 0, make([]contract.ValidatedSummaryPoint, MaxPageCards+1)); err == nil {
		t.Fatal("oversized page accepted")
	}
	builder.bytes = MaxSpoolBytes
	if err := builder.Append(context.Background(), 0, nil); err == nil {
		t.Fatal("spool quota ignored")
	}
	builder = New(storage, scope)
	if err := builder.Append(context.Background(), 0, []contract.ValidatedSummaryPoint{{Point: strings.Repeat("x", MaxCardBytes+1)}}); err == nil {
		t.Fatal("large card accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := builder.Append(ctx, 0, nil); err == nil {
		t.Fatal("cancel ignored")
	}
}
