package research_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/research"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canned is a minimal Perplexity-shaped response.
const canned = `{
  "model": "sonar-pro",
  "citations": ["https://a.example/1", "https://b.example/2"],
  "choices": [{"message": {"role": "assistant", "content": "The answer."}}],
  "usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
}`

func TestResearch_ParsesAnswerCitationsUsage(t *testing.T) {
	t.Parallel()

	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, canned)
	}))
	defer srv.Close()

	c := research.NewClient("sk-test", research.WithBaseURL(srv.URL))
	res, err := c.Research(context.Background(), research.Request{
		Query:   "what is search-as-code?",
		System:  "be concise",
		Recency: "week",
		Domains: []string{"arxiv.org"},
	})
	require.NoError(t, err, "Research should succeed against the mock server")

	assert.Equal(t, "The answer.", res.Answer)
	assert.Equal(t, []string{"https://a.example/1", "https://b.example/2"}, res.Citations)
	assert.Equal(t, 30, res.Usage.TotalTokens)
	assert.Equal(t, "Bearer sk-test", gotAuth, "API key must be sent as a bearer token")

	// Request shape: default model applied, system+user messages, filters passed through.
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(gotBody), &sent))
	assert.Equal(t, "sonar-pro", sent["model"], "empty model defaults to sonar-pro")
	assert.Equal(t, "week", sent["search_recency_filter"])
	msgs, ok := sent["messages"].([]any)
	require.True(t, ok)
	assert.Len(t, msgs, 2, "system + user messages")
}

func TestResearch_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     research.Request
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name:    "empty query",
			req:     research.Request{Query: "   "},
			wantErr: "empty query",
		},
		{
			name: "non-2xx surfaces status + body",
			req:  research.Request{Query: "x"},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, "bad key")
			},
			wantErr: "HTTP 401",
		},
		{
			name: "no choices",
			req:  research.Request{Query: "x"},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"choices": []}`)
			},
			wantErr: "no choices",
		},
	}

	for _, tc := range tests {
		tt := tc
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := []research.Option{}
			if tt.handler != nil {
				srv := httptest.NewServer(tt.handler)
				defer srv.Close()
				opts = append(opts, research.WithBaseURL(srv.URL))
			}
			_, err := research.NewClient("sk-test", opts...).Research(context.Background(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
