package tmux

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	tm := New()
	if tm == nil {
		t.Fatal("New() returned nil")
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"lw-dev", false},
		{"lw_prod", false},
		{"session123", false},
		{"ABC-xyz", false},
		{"", true},
		{"has space", true},
		{"has.dot", true},
		{"has/slash", true},
		{"has:colon", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.name)
			if tt.wantErr && err == nil {
				t.Errorf("validateName(%q) = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateName(%q) = %v, want nil", tt.name, err)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrInvalidName) {
				t.Errorf("validateName(%q) error = %v, want ErrInvalidName", tt.name, err)
			}
		})
	}
}

func TestValidateNameEmpty(t *testing.T) {
	// Subtests with empty name use t.Run("") which is valid but worth an explicit check
	err := validateName("")
	if err == nil {
		t.Fatal("validateName empty string should return error")
	}
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}
