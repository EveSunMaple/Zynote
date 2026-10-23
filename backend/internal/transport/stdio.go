package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const MaxFrameBytes = 8 * 1024 * 1024

type Envelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *WireError      `json:"error,omitempty"`
}

type WireError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type reverseReply struct {
	result json.RawMessage
	err    error
}

type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (w *Writer) Send(env Envelope) error {
	line, err := json.Marshal(env)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.w.Write(line)
	return err
}

type Server struct {
	handler   contract.Handler
	out       *Writer
	pending   sync.Map
	requestMu sync.Mutex
	nextID    int64
}

func NewServer(handler contract.Handler) *Server { return &Server{handler: handler} }

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = NewWriter(out)
	frames := make(chan []byte, 256)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 256*1024), MaxFrameBytes)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			frame := make([]byte, len(line))
			copy(frame, line)
			frames <- frame
		}
		readErr <- scanner.Err()
		close(frames)
	}()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				if err := <-readErr; err != nil {
					return err
				}
				return ctx.Err()
			}
			s.handleFrame(frame)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) handleFrame(frame []byte) {
	var env Envelope
	if err := json.Unmarshal(frame, &env); err != nil {
		slog.Warn("bad frame", "error", err)
		return
	}
	switch {
	case env.Method != "" && env.ID != nil:
		go func() {
			s.requestMu.Lock()
			defer s.requestMu.Unlock()
			s.handleRequest(env)
		}()
	case env.Method != "" && env.ID == nil:
	case env.Method == "" && env.ID != nil:
		s.handleReverseReply(env)
	default:
		slog.Warn("unknown frame shape")
	}
}

func (s *Server) handleRequest(env Envelope) {
	result, err := s.handler.Invoke(env.Method, env.Params)
	reply := Envelope{JSONRPC: "2.0", ID: env.ID}
	if err != nil {
		reply.Error = toWireError(err)
	} else {
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			reply.Error = toWireError(marshalErr)
		} else {
			reply.Result = raw
		}
	}
	if sendErr := s.out.Send(reply); sendErr != nil {
		slog.Error("send reply failed", "error", sendErr)
	}
}

func toWireError(err error) *WireError {
	if appErr, ok := err.(*contract.AppError); ok {
		data, _ := json.Marshal(appErr.Data)
		return &WireError{Code: appErr.Code, Message: appErr.Message, Data: data}
	}
	return &WireError{Code: contract.CodeInternal, Message: err.Error()}
}

func (s *Server) handleReverseReply(env Envelope) {
	var id string
	if err := json.Unmarshal(env.ID, &id); err != nil {
		slog.Warn("bad reverse id", "error", err)
		return
	}
	value, ok := s.pending.LoadAndDelete(id)
	if !ok {
		slog.Warn("unknown reverse id", "id", id)
		return
	}
	ch := value.(chan reverseReply)
	if env.Error != nil {
		ch <- reverseReply{err: fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)}
	} else {
		ch <- reverseReply{result: env.Result}
	}
}

func (s *Server) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	if s.out == nil {
		return nil, fmt.Errorf("transport not started")
	}
	id := fmt.Sprintf("s-%d", atomic.AddInt64(&s.nextID, 1))
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	rawID, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	ch := make(chan reverseReply, 1)
	s.pending.Store(id, ch)
	defer s.pending.Delete(id)
	if err := s.out.Send(Envelope{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-timer.C:
		return nil, fmt.Errorf("shell service %s timeout", method)
	}
}

func (s *Server) Notify(method string, params any) error {
	if s.out == nil {
		return fmt.Errorf("transport not started")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return s.out.Send(Envelope{JSONRPC: "2.0", Method: method, Params: raw})
}
