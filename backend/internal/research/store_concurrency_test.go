package research

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 后台研究持续写库时，新请求打开同一个库不能因短暂的锁而失败。
func TestOpenWhileBackgroundWrites(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := map[string]any{"text": strings.Repeat("字", 200000)}
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = writer.Put("task", "t1", index, payload)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reader, err := Open(root)
		if err != nil {
			close(stop)
			<-done
			t.Fatalf("opening during background writes failed: %v", err)
		}
		if _, err := reader.List("task"); err != nil {
			close(stop)
			<-done
			t.Fatalf("reading during background writes failed: %v", err)
		}
		reader.Close()
	}
	close(stop)
	<-done
}
