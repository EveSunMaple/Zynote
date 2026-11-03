package generationjob

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AcquireRunLock is an OS-owned lease, shared across Service instances and
// backend processes. Process death releases the lock without a stale timeout.
// The small lock file is retained; deleting a live lock inode would be unsafe.
func AcquireRunLock(root, jobID string) (func(), error) {
	if jobID == "" || strings.ContainsAny(jobID, "/\\:") || jobID == "." || jobID == ".." {
		return nil, ErrFence
	}
	directory := filepath.Join(root, "run-locks")
	if os.MkdirAll(directory, 0700) != nil {
		return nil, fmt.Errorf("任务执行锁不可用")
	}
	path := filepath.Join(directory, jobID+".lock")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("任务执行锁不可用")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("任务执行锁不可用")
	}
	if err := lockRunFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("任务正在其他执行实例中处理，请等待完成")
	}
	return func() { unlockRunFile(file); file.Close() }, nil
}
