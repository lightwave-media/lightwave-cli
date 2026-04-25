package cli

import (
	"reflect"
	"testing"
)

func TestComputeDrift(t *testing.T) {
	allowed := map[string]struct{}{
		"images":  {},
		"static":  {},
		"media":   {},
		"uploads": {},
		"fonts":   {},
		"js":      {},
		"css":     {},
	}

	tests := []struct {
		name     string
		bucket   []string
		expected []string
	}{
		{
			name:     "no drift",
			bucket:   []string{"images/", "static/", "media/"},
			expected: nil,
		},
		{
			name:     "drift only",
			bucket:   []string{"brands/", "generated/", "shared/"},
			expected: []string{"brands", "generated", "shared"},
		},
		{
			name:     "mixed",
			bucket:   []string{"images/", "brands/", "static/", "generated/"},
			expected: []string{"brands", "generated"},
		},
		{
			name:     "empty bucket",
			bucket:   nil,
			expected: nil,
		},
		{
			name:     "ignores empty prefix",
			bucket:   []string{"", "/", "brands/"},
			expected: []string{"brands"},
		},
		{
			name:     "drift sorted alphabetically",
			bucket:   []string{"zeta/", "alpha/", "mike/"},
			expected: []string{"alpha", "mike", "zeta"},
		},
		{
			name:     "trailing-slash and bare both treated equal",
			bucket:   []string{"images", "brands"},
			expected: []string{"brands"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDrift(tt.bucket, allowed)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("computeDrift() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFindInfraDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{
			name:     "top level",
			input:    map[string]any{"infrastructure_domain": "lightwave-media.ltd"},
			expected: "lightwave-media.ltd",
		},
		{
			name: "nested under product key",
			input: map[string]any{
				"foo": map[string]any{
					"bar": map[string]any{"infrastructure_domain": "example.com"},
				},
			},
			expected: "example.com",
		},
		{
			name:     "missing",
			input:    map[string]any{"foo": "bar"},
			expected: "",
		},
		{
			name:     "empty string ignored",
			input:    map[string]any{"infrastructure_domain": ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findInfraDomain(tt.input)
			if got != tt.expected {
				t.Errorf("findInfraDomain() = %q, want %q", got, tt.expected)
			}
		})
	}
}
