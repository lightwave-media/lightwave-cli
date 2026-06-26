//nolint:testpackage // white-box tests for unexported flatten helpers (matches cli_loader_test).
package sst

import "testing"

func TestFlattenCommandKeys_Nested(t *testing.T) {
	t.Parallel()

	cmds := []CLICommand{
		{
			Name:        "profile",
			Description: "profiles",
			Commands: []CLICommand{
				{Name: "list", Description: "list"},
				{Name: "validate", Description: "validate"},
			},
		},
		{Name: "speak", Description: "speak"},
	}

	keys := flattenCommandKeys("voice", "", cmds)
	want := []string{"voice.profile.list", "voice.profile.validate", "voice.speak"}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(keys), len(want), keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], k)
		}
	}
}

func TestValidateCommandTree_AllowsGroupWithoutLeafDescription(t *testing.T) {
	t.Parallel()

	cfg := &CLIConfig{
		Version: "1.0.0",
		Domains: []CLIDomain{{
			Name: "voice",
			Commands: []CLICommand{{
				Name:        "profile",
				Description: "group",
				Commands:    []CLICommand{{Name: "list", Description: "list profiles"}},
			}},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
