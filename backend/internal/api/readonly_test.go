package api

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

// 只读连接在 WAL 数据库没有活动连接时会一起初始化 -shm；并发打开必须等待
// 对方完成，不能偶发返回 database is locked。
func TestOpenReadOnlyDBConcurrentReaders(t *testing.T) {
	database := filepath.Join(t.TempDir(), assistantDBFile)
	write, err := storage.OpenWrite(database)
	if err != nil {
		t.Fatalf("open write db: %v", err)
	}
	if err := storage.BootstrapInto(write); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := write.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := write.Close(); err != nil {
		t.Fatalf("close write db: %v", err)
	}
	state := &contract.State{DataRoot: filepath.Dir(database)}
	for round := 0; round < 20; round++ {
		var wait sync.WaitGroup
		errs := make(chan error, 8)
		for reader := 0; reader < 8; reader++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				db, err := openReadOnlyDB(state)
				if err != nil {
					errs <- err
					return
				}
				defer db.Close()
				if _, err := storage.QueryAll(db, "SELECT * FROM courses"); err != nil {
					errs <- err
				}
			}()
		}
		wait.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}
