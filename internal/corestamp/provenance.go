package corestamp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// bindingsTagPrefix is the subdirectory prefix Go requires on module tags for a
// module nested at bindings/go (e.g. "bindings/go/v0.6.5").
const bindingsTagPrefix = "bindings/go/"

// TagVersion extracts the semantic version a lightwave-core module tag encodes.
// It accepts the nested-module form ("bindings/go/v0.6.5") and the bare release
// form ("v0.6.5"), returning "0.6.5" for both.
//
// A ref that is not a release tag — a branch, a raw SHA, "origin/main" — has no
// version to compare against and is reported as such rather than guessed at.
func TagVersion(tag string) (string, error) {
	name := strings.TrimPrefix(tag, bindingsTagPrefix)
	if !strings.HasPrefix(name, "v") {
		return "", fmt.Errorf("ref %q is not a release tag, so it carries no version", tag)
	}

	version := strings.TrimPrefix(name, "v")
	if version == "" || strings.ContainsAny(version, "/ ") {
		return "", fmt.Errorf("ref %q is not a release tag, so it carries no version", tag)
	}

	return version, nil
}

// VerifyVersionMatchesSourceTag asserts the embedded stamp declares the same
// version as the tag it was extracted from.
//
// These two facts come from different places — Version is lifted out of core's
// own loader.go, SourceTag is the ref the sync extracted — so a release tagged
// without its version bump makes them disagree. Comparing values a single hand
// bumps together (core's pyproject.toml against core's loader.go) cannot detect
// that class at all: both sides move, or neither does.
func VerifyVersionMatchesSourceTag() error {
	return verifyVersionMatchesTag(Version, SourceTag)
}

// verifyVersionMatchesTag is the comparison itself, taking both sides as
// arguments so the v0.6.5 case (tree 0.6.4, tag v0.6.5) is reproducible in a
// test without a checkout or a doctored const.
func verifyVersionMatchesTag(version, tag string) error {
	tagged, err := TagVersion(tag)
	if err != nil {
		return err
	}

	if tagged != version {
		return fmt.Errorf(
			"stamp version drift: embedded Version is %q but SourceTag %q encodes %q — "+
				"the tag was published without its version bump (lightwave-core#552), "+
				"or the mirror was synced from a ref that is not the pinned release",
			version, tag, tagged,
		)
	}

	return nil
}

// ComputeSchemasSHA256 digests the embedded schema tree. The digest is
// order-stable: paths are sorted, then each file contributes "path\x00bytes",
// so a rename is as visible as an edit.
func ComputeSchemasSHA256() (string, error) {
	var paths []string

	err := fs.WalkDir(schemaFS, schemaRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking embedded schemas: %w", err)
	}

	sort.Strings(paths)

	digest := sha256.New()

	for _, path := range paths {
		raw, readErr := schemaFS.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("reading embedded %s: %w", path, readErr)
		}

		// The NUL separator keeps "ab" + "c" distinguishable from "a" + "bc".
		digest.Write([]byte(strings.TrimPrefix(path, schemaRoot+"/")))
		digest.Write([]byte{0})
		digest.Write(raw)
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// VerifyEmbeddedDigest asserts the embedded tree still hashes to the digest the
// sync recorded, so a hand-edit under internal/corestamp/schemas is caught
// without needing a lightwave-core checkout.
func VerifyEmbeddedDigest() error {
	actual, err := ComputeSchemasSHA256()
	if err != nil {
		return err
	}

	return verifyDigest(SchemasSHA256, actual)
}

// verifyDigest is the comparison itself, taking both sides as arguments so a
// tampered mirror is reproducible in a test without editing the vendored tree.
func verifyDigest(recorded, actual string) error {
	if recorded == "" {
		return fmt.Errorf("no SchemasSHA256 recorded — run scripts/sync-core-stamp.sh")
	}

	if recorded != actual {
		return fmt.Errorf(
			"embedded schemas digest mismatch: recorded %s, computed %s — "+
				"the mirror was edited by hand; re-run scripts/sync-core-stamp.sh",
			shortDigest(recorded), shortDigest(actual),
		)
	}

	return nil
}

// shortDigest trims a hex digest for error messages. It takes any length so the
// caller never has to reason about whether a recorded digest is well-formed.
func shortDigest(digest string) string {
	const shown = 16
	if len(digest) <= shown {
		return digest
	}

	return digest[:shown] + "…"
}
