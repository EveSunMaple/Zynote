// Package summarytree stores bounded immutable pages and reconstructs every
// leaf disposition. It performs no model calls or semantic completeness claim.
package summarytree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const (
	MaxNodeBytes  = 256 << 10
	MaxCardBytes  = 64 << 10
	MaxPageCards  = 32
	MaxSpoolBytes = 32 << 20
	MaxNodes      = 8192
	MaxDepth      = 8
	Fanout        = 4
	MaxLeaves     = 65536
)

type Storage interface {
	Put(context.Context, []byte) (string, error)
	Get(context.Context, string) ([]byte, error)
}
type Scope struct {
	SourceSHA256, RecipeSHA256 string
	Generation, Epoch          int64
}
type Leaf struct {
	ID                  string
	Chunk, Ordinal      int
	Point               contract.ValidatedSummaryPoint
	Disposition, Reason string
}
type Node struct {
	Version  int
	Scope    Scope
	Depth    int
	Chunk    int
	Children []string
	Leaves   []Leaf
}
type Result struct {
	Root                             string
	LeafCount, PageCount, NodeCount  int
	LeafSetSHA256, DispositionSHA256 string
}
type Builder struct {
	storage                                Storage
	scope                                  Scope
	refs                                   []string
	bytes, nodes, leaves, pages, nextChunk int
}

func Digest(body []byte) string                 { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func hashValue(value any) string                { body, _ := json.Marshal(value); return Digest(body) }
func failure() error                            { return fmt.Errorf("摘要分页、谱系或资源限制校验失败") }
func New(storage Storage, scope Scope) *Builder { return &Builder{storage: storage, scope: scope} }
func (b *Builder) put(ctx context.Context, node Node) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	body, err := json.Marshal(node)
	if err != nil || len(body) > MaxNodeBytes || b.nodes >= MaxNodes || b.bytes+len(body) > MaxSpoolBytes {
		return "", failure()
	}
	ref, err := b.storage.Put(ctx, body)
	if err != nil {
		return "", err
	}
	if ref != Digest(body) {
		return "", failure()
	}
	b.bytes += len(body)
	b.nodes++
	return ref, nil
}

// Append requires ordered chunk commits. Pending scheduler results do not
// advance this index, including when a success is already durable in cache.
func (b *Builder) Append(ctx context.Context, chunk int, points []contract.ValidatedSummaryPoint) error {
	if chunk != b.nextChunk || len(points) > MaxPageCards || b.leaves+len(points) > MaxLeaves {
		return failure()
	}
	leaves := make([]Leaf, len(points))
	for i, point := range points {
		encoded, err := json.Marshal(point)
		if err != nil || len(encoded) > MaxCardBytes {
			return failure()
		}
		leaf := Leaf{Chunk: chunk, Ordinal: i, Point: point, Disposition: "consumed"}
		leaf.ID = hashValue(struct {
			Source, Recipe string
			Chunk, Ordinal int
			Point          contract.ValidatedSummaryPoint
		}{b.scope.SourceSHA256, b.scope.RecipeSHA256, chunk, i, point})
		leaves[i] = leaf
	}
	ref, err := b.put(ctx, Node{Version: 1, Scope: b.scope, Chunk: chunk, Leaves: leaves})
	if err != nil {
		return err
	}
	b.refs = append(b.refs, ref)
	b.pages++
	b.leaves += len(leaves)
	b.nextChunk++
	return nil
}
func (b *Builder) Finish(ctx context.Context, visit func(Leaf) error) (Result, error) {
	if len(b.refs) == 0 {
		return Result{}, failure()
	}
	refs := append([]string(nil), b.refs...)
	depth := 0
	for len(refs) > 1 {
		depth++
		if depth > MaxDepth {
			return Result{}, failure()
		}
		next := []string{}
		for offset := 0; offset < len(refs); offset += Fanout {
			end := offset + Fanout
			if end > len(refs) {
				end = len(refs)
			}
			ref, err := b.put(ctx, Node{Version: 1, Scope: b.scope, Chunk: -1, Depth: depth, Children: refs[offset:end]})
			if err != nil {
				return Result{}, err
			}
			next = append(next, ref)
		}
		refs = next
	}
	return Verify(ctx, b.storage, b.scope, refs[0], visit)
}

type walkState struct {
	bytes, nodes, leaves, pages int
	seenNodes, seenLeaves       map[string]bool
	leafHash, dispositionHash   hash.Hash
	lastChunk, lastOrdinal      int
}

func Verify(ctx context.Context, storage Storage, scope Scope, root string, visit func(Leaf) error) (Result, error) {
	state := walkState{seenNodes: map[string]bool{}, seenLeaves: map[string]bool{}, leafHash: sha256.New(), dispositionHash: sha256.New(), lastChunk: -1, lastOrdinal: -1}
	var walk func(string, int) (int, error)
	walk = func(ref string, level int) (int, error) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if level > MaxDepth || state.nodes >= MaxNodes || len(ref) != 64 || state.seenNodes[ref] {
			return 0, failure()
		}
		state.seenNodes[ref] = true
		body, err := storage.Get(ctx, ref)
		if err != nil {
			return 0, err
		}
		if len(body) > MaxNodeBytes || Digest(body) != ref || state.bytes+len(body) > MaxSpoolBytes {
			return 0, failure()
		}
		state.bytes += len(body)
		state.nodes++
		var node Node
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&node) != nil || decoder.Decode(&struct{}{}) != io.EOF || node.Version != 1 || node.Scope != scope || node.Depth < 0 || node.Depth > MaxDepth {
			return 0, failure()
		}
		if len(node.Children) > 0 {
			if len(node.Children) > Fanout || len(node.Leaves) != 0 || node.Depth == 0 || node.Chunk != -1 {
				return 0, failure()
			}
			for _, child := range node.Children {
				depth, err := walk(child, level+1)
				if err != nil {
					return 0, err
				}
				if depth != node.Depth-1 {
					return 0, failure()
				}
			}
			return node.Depth, nil
		}
		if node.Depth != 0 || len(node.Leaves) > MaxPageCards || node.Chunk != state.pages {
			return 0, failure()
		}
		state.pages++
		for _, leaf := range node.Leaves {
			if state.leaves >= MaxLeaves || state.seenLeaves[leaf.ID] || leaf.Chunk != node.Chunk || leaf.Ordinal < 0 {
				return 0, failure()
			}
			if leaf.Chunk < state.lastChunk || (leaf.Chunk == state.lastChunk && leaf.Ordinal != state.lastOrdinal+1) || (leaf.Chunk > state.lastChunk && leaf.Ordinal != 0) {
				return 0, failure()
			}
			encoded, _ := json.Marshal(leaf.Point)
			if len(encoded) > MaxCardBytes || leaf.ID != hashValue(struct {
				Source, Recipe string
				Chunk, Ordinal int
				Point          contract.ValidatedSummaryPoint
			}{scope.SourceSHA256, scope.RecipeSHA256, leaf.Chunk, leaf.Ordinal, leaf.Point}) {
				return 0, failure()
			}
			if (leaf.Disposition != "consumed" && leaf.Disposition != "omitted") || (leaf.Disposition == "omitted" && leaf.Reason == "") || (leaf.Disposition == "consumed" && leaf.Reason != "") {
				return 0, failure()
			}
			state.seenLeaves[leaf.ID] = true
			state.leaves++
			state.lastChunk, state.lastOrdinal = leaf.Chunk, leaf.Ordinal
			_, _ = state.leafHash.Write([]byte(leaf.ID + "\n"))
			disposition, _ := json.Marshal(struct{ ID, State, Reason string }{leaf.ID, leaf.Disposition, leaf.Reason})
			_, _ = state.dispositionHash.Write(append(disposition, '\n'))
			if visit != nil {
				if err := visit(leaf); err != nil {
					return 0, err
				}
			}
		}
		return 0, nil
	}
	if _, err := walk(root, 0); err != nil {
		return Result{}, err
	}
	return Result{Root: root, LeafCount: state.leaves, PageCount: state.pages, NodeCount: state.nodes, LeafSetSHA256: hex.EncodeToString(state.leafHash.Sum(nil)), DispositionSHA256: hex.EncodeToString(state.dispositionHash.Sum(nil))}, nil
}
