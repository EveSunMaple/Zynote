package textbook

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

type fileServer struct {
	mu       sync.Mutex
	listener net.Listener
	paths    map[string]string
}

func (server *fileServer) URL(path string) (string, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.paths == nil {
		server.paths = map[string]string{}
	}
	if server.listener == nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("教材预览启动失败")
		}
		server.listener = listener
		go func() {
			_ = http.Serve(listener, server)
		}()
	}
	token := ids.New()
	server.paths[token] = path
	return "http://" + server.listener.Addr().String() + "/" + token, nil
}

func (server *fileServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	token := strings.TrimPrefix(strings.SplitN(request.URL.Path, "?", 2)[0], "/")
	server.mu.Lock()
	path := server.paths[token]
	server.mu.Unlock()
	if path == "" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges,Content-Length,Content-Range")
	writer.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS")
	writer.Header().Set("Accept-Ranges", "bytes")
	writer.Header().Set("Content-Type", "application/pdf")
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodOptions {
		writer.Header().Set("Access-Control-Allow-Headers", "Range")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(writer, request)
		return
	}
	http.ServeContent(writer, request, filepath.Base(path), info.ModTime().Truncate(time.Second), file)
}
