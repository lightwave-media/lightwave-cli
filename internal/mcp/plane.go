package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

const (
	defaultNullhubBase = "http://127.0.0.1:19800"
	defaultHTTPTimeout = 3 * time.Second
	nullhubProbePath   = "/health"
)

// PlaneDown is the structured error ADR-0002 requires for a down runtime plane.
type PlaneDown struct {
	Error  string `json:"error"`
	Plane  string `json:"plane"`
	Detail string `json:"detail,omitempty"`
}

func planeDown(plane, detail string) PlaneDown {
	return PlaneDown{Error: "runtime_plane_down", Plane: plane, Detail: detail}
}

func encodePlaneDown(p PlaneDown) string {
	b, err := json.Marshal(p)
	if err != nil {
		return `{"error":"runtime_plane_down"}`
	}

	return string(b)
}

// NullhubBase is the control-plane URL (NULLHUB_BASE_URL or localhost:19800).
func NullhubBase() string {
	if v := os.Getenv("NULLHUB_BASE_URL"); v != "" {
		return v
	}

	return defaultNullhubBase
}

func probeNullhub(ctx context.Context, client *http.Client, base string) error {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+nullhubProbePath, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return nil
}

func connectDB(ctx context.Context) (*pgxpool.Pool, error) {
	return db.Connect(ctx)
}
