package corestamp

import (
	"strings"
	"testing"
)

func TestTagVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tag     string
		want    string
		wantErr bool
	}{
		{name: "nested module tag", tag: "bindings/go/v0.6.5", want: "0.6.5"},
		{name: "bare release tag", tag: "v0.6.4", want: "0.6.4"},
		{name: "prerelease suffix", tag: "bindings/go/v1.2.3-rc1", want: "1.2.3-rc1"},
		{name: "branch is not a release", tag: "origin/main", wantErr: true},
		{name: "raw sha is not a release", tag: "d3f89f8", wantErr: true},
		{name: "empty", tag: "", wantErr: true},
		{name: "prefix with no version", tag: "bindings/go/", wantErr: true},
	}

	for _, testCase := range cases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := TagVersion(tc.tag)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("TagVersion(%q) = %q, want an error", tc.tag, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("TagVersion(%q): %v", tc.tag, err)
			}

			if got != tc.want {
				t.Errorf("TagVersion(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}

func TestVerifyVersionMatchesTag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		version string
		tag     string
		wantErr bool
	}{
		{name: "coherent nested tag", version: "0.6.5", tag: "bindings/go/v0.6.5"},
		{name: "coherent bare tag", version: "0.6.4", tag: "v0.6.4"},
		{
			// The live lightwave-core defect: v0.6.5 was tagged while the tree
			// still declared 0.6.4, so bindings/go/v0.6.5 publishes a module
			// self-reporting the previous version (lightwave-core#552).
			name:    "tag published without its version bump",
			version: "0.6.4",
			tag:     "bindings/go/v0.6.5",
			wantErr: true,
		},
		{name: "synced from a branch, not a release", version: "0.6.4", tag: "origin/main", wantErr: true},
	}

	for _, testCase := range cases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := verifyVersionMatchesTag(tc.version, tc.tag)

			if tc.wantErr && err == nil {
				t.Fatalf("verifyVersionMatchesTag(%q, %q) = nil, want an error", tc.version, tc.tag)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("verifyVersionMatchesTag(%q, %q): %v", tc.version, tc.tag, err)
			}
		})
	}
}

// TestOldLockstepComparisonMissesUnbumpedTag pins WHY the previous guard was
// replaced rather than merely un-skipped.
//
// The old TestVersionLockstep compared lightwave-core's pyproject.toml against
// lightwave-core's bindings/go/loader.go. One hand bumps both, so they move
// together or not at all — and when v0.6.5 was tagged without the bump, both
// sides read 0.6.4 and the comparison was satisfied. Repairing only its
// t.Skip would have produced a guard that still passed on the live defect.
//
// This test asserts that contrast directly: identical inputs, old comparison
// clean, new comparison failing.
func TestOldLockstepComparisonMissesUnbumpedTag(t *testing.T) {
	t.Parallel()

	// The state of lightwave-core at tag v0.6.5.
	const (
		pyprojectVersion = "0.6.4" // what pyproject.toml declared
		loaderVersion    = "0.6.4" // what bindings/go/loader.go declared
		publishedTag     = "bindings/go/v0.6.5"
	)

	// The old comparison: two hand-maintained copies of the same claim.
	if pyprojectVersion != loaderVersion {
		t.Fatal("fixture is wrong: the point is that these two agreed")
	}

	// The new comparison brings in the tag, which no hand had to remember.
	err := verifyVersionMatchesTag(loaderVersion, publishedTag)
	if err == nil {
		t.Fatal("new comparison passed on the v0.6.5 case; it must fail")
	}

	if !strings.Contains(err.Error(), "0.6.5") {
		t.Errorf("error should name the version the tag encodes, got: %v", err)
	}
}

// TestVerifyVersionMatchesSourceTag is the live assertion for this repo's own
// mirror. It needs no lightwave-core checkout — both sides are compiled in — so
// it runs everywhere, including this public repo's CI, and never skips.
func TestVerifyVersionMatchesSourceTag(t *testing.T) {
	t.Parallel()

	if err := VerifyVersionMatchesSourceTag(); err != nil {
		t.Fatalf("embedded stamp provenance is incoherent: %v", err)
	}
}

// TestVerifyEmbeddedDigest catches a hand-edit of internal/corestamp/schemas.
// Also checkout-free, so it too always runs.
func TestVerifyEmbeddedDigest(t *testing.T) {
	t.Parallel()

	if err := VerifyEmbeddedDigest(); err != nil {
		t.Fatalf("embedded schemas do not match their recorded digest: %v", err)
	}
}

// TestVerifyDigest proves the digest guard is capable of failing. A guard only
// ever exercised on good input is indistinguishable from one that cannot fire —
// which is precisely how the two skipping guards this change replaced went
// unnoticed (lightwave-cli#383). The tampered case is a fixture rather than a
// real edit to the vendored tree, so the proof is permanent and side-effect free.
func TestVerifyDigest(t *testing.T) {
	t.Parallel()

	const real = "b454394e6e9fe9cbc14cd181e2aad4a45577d983eb21f772d126b6b3da1e0c35"

	cases := []struct {
		name     string
		recorded string
		actual   string
		wantErr  string
	}{
		{name: "matching", recorded: real, actual: real},
		{
			name:     "mirror edited by hand",
			recorded: real,
			actual:   "0000000000000000000000000000000000000000000000000000000000000000",
			wantErr:  "digest mismatch",
		},
		{
			name:     "never synced",
			recorded: "",
			actual:   real,
			wantErr:  "no SchemasSHA256 recorded",
		},
	}

	for _, testCase := range cases {
		tc := testCase

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := verifyDigest(tc.recorded, tc.actual)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyDigest: unexpected error: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("verifyDigest = nil, want an error containing %q", tc.wantErr)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestComputeSchemasSHA256IsStable(t *testing.T) {
	t.Parallel()

	first, err := ComputeSchemasSHA256()
	if err != nil {
		t.Fatalf("ComputeSchemasSHA256: %v", err)
	}

	second, err := ComputeSchemasSHA256()
	if err != nil {
		t.Fatalf("ComputeSchemasSHA256 (second call): %v", err)
	}

	if first != second {
		t.Errorf("digest is not stable across calls: %s vs %s", first, second)
	}

	if len(first) != 64 {
		t.Errorf("expected a 64-char sha256 hex digest, got %d chars", len(first))
	}
}
