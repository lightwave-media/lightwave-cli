// Package mcp implements `lw mcp serve`: a stdio MCP server with
// persona-tier tool filtering (ADR LWAI-0002).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	protocolVersion     = "2024-11-05"
	jsonRPCVersion      = "2.0"
	contentLengthHeader = "content-length:"
	parseErrorCode      = -32700
	methodNotFoundCode  = -32601
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      json.RawMessage `json:"id,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	Error   *rpcError `json:"error,omitempty"`
	Result  any       `json:"result,omitempty"`
	ID      any       `json:"id"`
	JSONRPC string    `json:"jsonrpc"`
}

type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Server is the stdio MCP loop. Fields are overridable in tests.
type Server struct {
	Connect func(context.Context) (*pgxpool.Pool, error)
	Client  *http.Client
	Persona string
	HomeDir string
	Base    string
}

// Serve reads Content-Length framed JSON-RPC from in until EOF.
func Serve(ctx context.Context, in io.Reader, out io.Writer, s Server) error {
	if s.HomeDir == "" {
		s.HomeDir, _ = os.UserHomeDir()
	}

	if s.Base == "" {
		s.Base = NullhubBase()
	}

	if s.Client == nil {
		s.Client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	if s.Connect == nil {
		s.Connect = connectDB
	}

	tier := ResolveTier(s.HomeDir, s.Persona)
	r := bufio.NewReader(in)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		msg, err := readFrame(r)
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		var req rpcRequest

		if err := json.Unmarshal(msg, &req); err != nil {
			if writeErr := writeParseError(out); writeErr != nil {
				return writeErr
			}

			continue
		}

		if req.ID == nil {
			continue
		}

		if err := writeFrame(out, s.handle(ctx, tier, &req)); err != nil {
			return err
		}
	}
}

func writeParseError(out io.Writer) error {
	return writeFrame(out, rpcResponse{
		JSONRPC: jsonRPCVersion,
		Error:   &rpcError{Code: parseErrorCode, Message: "parse error"},
	})
}

func (s Server) handle(ctx context.Context, tier Tier, req *rpcRequest) rpcResponse {
	id := decodeID(req.ID)

	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "lightwave", "version": "1.0.0"},
		}}
	case "ping":
		return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{}}
	case "tools/list":
		return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{"tools": toolsFor(tier)}}
	case "tools/call":
		result := s.callTool(ctx, tier, req.Params)

		return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result}
	default:
		return rpcResponse{
			JSONRPC: jsonRPCVersion,
			ID:      id,
			Error:   &rpcError{Code: methodNotFoundCode, Message: "method not found"},
		}
	}
}

func decodeID(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}

	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}

	return nil
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	var contentLength int

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}

		n, ok, err := parseContentLength(trimmed)
		if err != nil {
			return nil, err
		}

		if ok {
			contentLength = n
		}
	}

	if contentLength <= 0 {
		return nil, errors.New("mcp: missing content-length")
	}

	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	return buf, nil
}

func parseContentLength(header string) (int, bool, error) {
	lower := strings.ToLower(header)

	after, ok := strings.CutPrefix(lower, contentLengthHeader)
	if !ok {
		return 0, false, nil
	}

	n, err := strconv.Atoi(strings.TrimSpace(after))
	if err != nil {
		return 0, false, fmt.Errorf("mcp: bad content-length: %w", err)
	}

	return n, true, nil
}

func writeFrame(w io.Writer, resp rpcResponse) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)

	return err
}
