package subtitlederived

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

type treeStorage struct {
	service *Service
	task    Task
	index   int
}

func (storage treeStorage) Put(ctx context.Context, body []byte) (string, error) {
	if storage.task.ExecutionGeneration > 0 {
		ledger, err := generationjob.Open(filepath.Join(storage.service.Root, "job-state"))
		if err != nil {
			return "", err
		}
		err = ledger.ReserveSpool(ctx, generationjob.Fence{JobID: storage.task.ID, Generation: storage.task.ExecutionGeneration, Epoch: storage.task.CacheEpoch}, fmt.Sprintf("%d:%s", storage.index, summarytree.Digest(body)), int64(len(body)), 2*summarytree.MaxSpoolBytes)
		_ = ledger.Close()
		if err != nil {
			return "", err
		}
	}
	var hash string
	err := storage.service.withTaskFence(ctx, storage.task, func() error {
		directory, err := evidenceDirectory(storage.service.Root, storage.task.ID, storage.index)
		if err != nil {
			return err
		}
		hash, err = publishPublicationBytes(ctx, directory, "blob", body, summarytree.MaxNodeBytes)
		return err
	})
	return hash, err
}
func (storage treeStorage) Get(ctx context.Context, hash string) ([]byte, error) {
	directory, err := locateEvidenceDirectory(storage.service.Root, storage.task.ID, storage.index, false)
	if err != nil {
		return nil, err
	}
	path, err := publicationBlobPath(directory, "blob", hash)
	if err != nil {
		return nil, err
	}
	return readPublicationBytes(ctx, filepath.Clean(path), hash, summarytree.MaxNodeBytes)
}

type summaryTreeAdapter struct {
	builder *summarytree.Builder
	scope   summarytree.Scope
}

func (tree *summaryTreeAdapter) Append(ctx context.Context, index int, points []contract.ValidatedSummaryPoint) error {
	return tree.builder.Append(ctx, index, points)
}
func (tree *summaryTreeAdapter) Finish(ctx context.Context, visit func(contract.ValidatedSummaryPoint) error) (contract.SummaryTreeEvidence, error) {
	result, err := tree.builder.Finish(ctx, func(leaf summarytree.Leaf) error { return visit(leaf.Point) })
	if err != nil {
		return contract.SummaryTreeEvidence{}, err
	}
	return contract.SummaryTreeEvidence{RootSHA256: result.Root, LeafSetSHA256: result.LeafSetSHA256, DispositionSHA256: result.DispositionSHA256, Generation: tree.scope.Generation, Epoch: tree.scope.Epoch, Leaves: result.LeafCount, Pages: result.PageCount, Nodes: result.NodeCount}, nil
}
func (service *Service) withSummaryTree(ctx context.Context, task *Task, item *Item) context.Context {
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	scope := summarytree.Scope{SourceSHA256: item.Source.Hash, RecipeSHA256: item.Recipe.Hash, Generation: task.ExecutionGeneration, Epoch: task.CacheEpoch}
	return contract.WithSummaryTree(ctx, &summaryTreeAdapter{builder: summarytree.New(treeStorage{service, *task, index}, scope), scope: scope})
}
func (service *Service) verifySummaryTree(ctx context.Context, task *Task, item *Item, rows []contract.ValidatedSummaryPoint, facts *contract.SummaryEvidence, audit AttemptAudit) error {
	if facts.TreeRootSHA256 == "" {
		if item.Recipe != nil && item.Recipe.ModeRecipe.Version >= 3 {
			return observerError()
		}
		return nil
	} // Frozen historical receipts have no tree.
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 {
		return observerError()
	}
	offset := 0
	chunkCursor, ordinal := 0, 0
	scope := summarytree.Scope{SourceSHA256: item.Source.Hash, RecipeSHA256: item.Recipe.Hash, Generation: facts.TreeGeneration, Epoch: facts.TreeEpoch}
	result, err := summarytree.Verify(ctx, treeStorage{service, *task, index}, scope, facts.TreeRootSHA256, func(leaf summarytree.Leaf) error {
		for chunkCursor < len(audit.validated) && ordinal == audit.validated[chunkCursor].PointCount {
			chunkCursor++
			ordinal = 0
		}
		if chunkCursor >= len(audit.validated) || leaf.Chunk != audit.validated[chunkCursor].Index || leaf.Ordinal != ordinal || offset >= len(rows) || leaf.Point != rows[offset] || leaf.Disposition != "consumed" {
			return observerError()
		}
		offset++
		ordinal++
		return nil
	})
	if err != nil || offset != len(rows) || result.PageCount != audit.TotalChunks || result.PageCount != facts.TreePages || result.NodeCount != facts.TreeNodes || result.LeafSetSHA256 != facts.TreeLeafSetSHA256 || result.DispositionSHA256 != facts.TreeDispositionSHA256 {
		return observerError()
	}
	return nil
}
