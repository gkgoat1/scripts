// Package daemon provides AgentWeave's private local control plane.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gkgoat1/scripts/agentweave/core"
)

type Service struct {
	Index   *core.Index
	Options core.ScanOptions
}

func (s *Service) Sync(ctx context.Context) ([]core.SourceStatus, error) {
	artifacts, statuses := core.ScanAll(ctx, s.Options)
	return s.Index.Sync(ctx, artifacts, statuses)
}

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Server struct {
	service   *Service
	socket    string
	listener  net.Listener
	closeOnce sync.Once
}

func Listen(service *Service, socket string) (*Server, error) {
	if service == nil || service.Index == nil {
		return nil, fmt.Errorf("daemon service requires an index")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket %s", socket)
		}
		if err := os.Remove(socket); err != nil {
			return nil, fmt.Errorf("remove stale daemon socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return &Server{service: service, socket: socket, listener: listener}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveConnection(ctx, connection)
	}
}

func (s *Server) ServeWithPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	_, _ = s.service.Sync(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.service.Sync(ctx)
			}
		}
	}()
	return s.Serve(ctx)
}

func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.listener.Close()
		_ = os.Remove(s.socket)
	})
	return err
}

func (s *Server) serveConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	decoder := json.NewDecoder(connection)
	encoder := json.NewEncoder(connection)
	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			return
		}
		response := s.handle(ctx, request)
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
}

type readParams struct {
	Workspace            string   `json:"workspace"`
	Refs                 []string `json:"refs"`
	MaxBytes             int      `json:"max_bytes,omitempty"`
	IncludeUserWorkflows bool     `json:"include_user_workflows,omitempty"`
}

func (s *Server) handle(ctx context.Context, request Request) Response {
	response := Response{ID: request.ID}
	decode := func(target any) bool {
		if err := json.Unmarshal(request.Params, target); err != nil {
			response.Error = "invalid parameters: " + err.Error()
			return false
		}
		return true
	}
	var result any
	var err error
	switch request.Method {
	case "sync":
		result, err = s.service.Sync(ctx)
	case "search":
		var params core.SearchRequest
		if !decode(&params) {
			return response
		}
		result, err = s.service.Index.Search(ctx, params)
	case "read":
		var params readParams
		if !decode(&params) {
			return response
		}
		result, err = s.service.Index.ReadScopedWithUser(ctx, params.Workspace, params.Refs, params.MaxBytes, false, params.IncludeUserWorkflows)
	case "dossier":
		var params core.SynthesisRequest
		if !decode(&params) {
			return response
		}
		result, err = s.service.Index.Dossier(ctx, params)
	case "status":
		result, err = s.service.Index.Status(ctx)
	case "stats":
		result, err = s.service.Index.DebugStats(ctx)
	default:
		response.Error = "unknown AgentWeave method: " + request.Method
		return response
	}
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.Result = result
	return response
}

type Client struct {
	Socket string
}

func (c Client) Call(ctx context.Context, method string, params any, target any) error {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(connection).Encode(Request{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Method: method, Params: data}); err != nil {
		return err
	}
	var response Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if target == nil {
		return nil
	}
	data, err = json.Marshal(response.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (c Client) Search(ctx context.Context, request core.SearchRequest) ([]core.SearchResult, error) {
	var result []core.SearchResult
	err := c.Call(ctx, "search", request, &result)
	return result, err
}

func (c Client) Read(ctx context.Context, workspace string, refs []string, maxBytes int) ([]core.SearchResult, error) {
	return c.ReadWithUser(ctx, workspace, refs, maxBytes, false)
}

func (c Client) ReadWithUser(ctx context.Context, workspace string, refs []string, maxBytes int, includeUserWorkflows bool) ([]core.SearchResult, error) {
	var result []core.SearchResult
	err := c.Call(ctx, "read", readParams{Workspace: workspace, Refs: refs, MaxBytes: maxBytes, IncludeUserWorkflows: includeUserWorkflows}, &result)
	return result, err
}

func (c Client) Dossier(ctx context.Context, request core.SynthesisRequest) (core.EvidenceDossier, error) {
	var result core.EvidenceDossier
	err := c.Call(ctx, "dossier", request, &result)
	return result, err
}

func (c Client) Status(ctx context.Context) ([]core.SourceStatus, error) {
	var result []core.SourceStatus
	err := c.Call(ctx, "status", struct{}{}, &result)
	return result, err
}
