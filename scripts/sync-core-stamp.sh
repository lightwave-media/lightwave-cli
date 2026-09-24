#!/usr/bin/env bash
# Refresh internal/corestamp from a lightwave-core checkout.
#
# lightwave-cli is public; lightwave-core is private. Importing
# github.com/lightwave-media/lightwave-core/bindings/go as a module breaks
# `go build`/`go install` for anyone outside the org and forces a
# private-module token into every CI job, so the binding is vendored instead.
#
# The cost of vendoring is staleness: internal/corestamp is a snapshot and can
# lag the canonical stamp. This script is the single supported way to refresh
# it. Run it after any change under lightwave-core/src/schemas that lw needs to
# see without a checkout.
#
# Content is read from a git REF, never from the source working tree. A working
# tree is whatever branch another agent happens to have out, dirty included —
# syncing from one embedded an unrelated session's in-flight state into this
# public repo (lightwave-cli#383). `--ref` names the commit-ish to extract; the
# recorded SourceTag is what the guards in loader_test.go assert against.
#
# Usage:
#   scripts/sync-core-stamp.sh [--ref <commit-ish>] [--allow-dirty] [path-to-lightwave-core]
#
# Defaults: --ref resolves to the bindings_tag pinned for lightwave-core in
# ~/.lightwave/config/releases.toml, falling back to origin/main. The checkout
# defaults to $LW_LIGHTWAVE_ROOT/lightwave-core, else a sibling ../lightwave-core.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dst="$repo/internal/corestamp"
pins="${LW_RELEASES_TOML:-$HOME/.lightwave/config/releases.toml}"

ref=""
allow_dirty=0
core=""

while [ $# -gt 0 ]; do
  case "$1" in
    --ref) ref="${2:?--ref needs a commit-ish}"; shift 2 ;;
    --allow-dirty) allow_dirty=1; shift ;;
    -h|--help) sed -n '2,26p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) core="$1"; shift ;;
  esac
done

if [ -z "$core" ]; then
  core="${LW_LIGHTWAVE_ROOT:+$LW_LIGHTWAVE_ROOT/lightwave-core}"
  core="${core:-$repo/../lightwave-core}"
fi

[ -d "$core/.git" ] || git -C "$core" rev-parse --git-dir >/dev/null 2>&1 || {
  echo "no lightwave-core checkout at $core" >&2
  echo "pass the checkout path: scripts/sync-core-stamp.sh /path/to/lightwave-core" >&2
  exit 1
}

# The pinned bindings_tag is the default source of truth for what to sync.
pinned_tag() {
  [ -f "$pins" ] || return 0
  awk '
    /^\[lightwave-core\]/ { in_section = 1; next }
    /^\[/                 { in_section = 0 }
    in_section && /^[[:space:]]*bindings_tag[[:space:]]*=/ {
      gsub(/.*=[[:space:]]*"?|"?[[:space:]]*$/, ""); print; exit
    }
  ' "$pins"
}

if [ -z "$ref" ]; then
  ref="$(pinned_tag)"
  if [ -n "$ref" ]; then
    echo "ref not given — using bindings_tag pinned in $pins: $ref"
  else
    ref="origin/main"
    echo "ref not given and no pin found in $pins — using $ref"
  fi
fi

git -C "$core" rev-parse --verify --quiet "$ref^{commit}" >/dev/null || {
  echo "ref '$ref' does not resolve in $core" >&2
  exit 1
}
sha="$(git -C "$core" rev-parse "$ref^{commit}")"

# A ref that is not a tag can still be a moving target; a dirty source tree is
# irrelevant now that content comes from the object store, but flag it so the
# operator knows the checkout they are looking at is not what got synced.
if [ -n "$(git -C "$core" status --porcelain)" ] && [ "$allow_dirty" -eq 0 ]; then
  echo "note: $core has uncommitted changes; syncing from $ref ($(git -C "$core" rev-parse --short "$sha")) regardless — the working tree is not read."
fi

# Where the schemas and the declared version live AT THE REF. Releases up to
# bindings/go/v0.8.0 carried a Go binding (bindings/go/schemas, Version in
# bindings/go/loader.go); lightwave-core retired it, and from v0.9.0 the only
# copy is src/schemas with the version in pyproject.toml. Decide per ref, so
# an old tag still syncs and a new one does not need a binding that is gone.
if git -C "$core" cat-file -e "$ref:bindings/go/schemas" 2>/dev/null; then
  schemas_path="bindings/go/schemas"
  version="$(git -C "$core" show "$ref:bindings/go/loader.go" \
    | sed -n 's/^const Version = "\(.*\)"$/\1/p')"
  version_source="bindings/go/loader.go"
else
  schemas_path="src/schemas"
  version="$(git -C "$core" show "$ref:pyproject.toml" \
    | sed -n 's/^version = "\(.*\)"$/\1/p' | head -1)"
  version_source="pyproject.toml"
fi
strip="$(printf '%s' "$schemas_path" | awk -F/ '{print NF}')"

# Atomic swap: stage beside the target, then replace — so a mid-extract failure
# (disk full, perms) leaves the existing mirror intact rather than half-gone.
rm -rf "${dst}/schemas.tmp"
mkdir -p "${dst}/schemas.tmp"
git -C "$core" archive "$ref" -- "$schemas_path" \
  | tar -x -C "${dst}/schemas.tmp" --strip-components="$strip"
[ -n "$(ls -A "${dst}/schemas.tmp")" ] || {
  echo "extracted nothing from $ref:$schemas_path" >&2
  rm -rf "${dst}/schemas.tmp"
  exit 1
}

# Three facts travel with the schemas, all read from the ref — never the tree:
#   Version       what core declares its release to be ($version_source)
#   SourceTag     the ref this mirror was extracted from
#   SchemasSHA256 digest of the embedded tree, so a hand-edit is detectable
# Version and SourceTag come from independent places on purpose: comparing them
# is what catches a tag published without its version bump (lightwave-core#552).
[ -n "$version" ] || { echo "could not read the version from $ref:$version_source" >&2; exit 1; }

# Digest is order-stable: sort by path, hash "path\0bytes" per file. Paths are
# relative to schemas/ with no "./" prefix, matching ComputeSchemasSHA256 in
# provenance.go — that function is authoritative and the guard compares to it.
schemas_sha="$(cd "${dst}/schemas.tmp" && find . -type f -print0 \
  | LC_ALL=C sort -z \
  | while IFS= read -r -d '' f; do printf '%s\0' "${f#./}"; cat "$f"; done \
  | shasum -a 256 | cut -d' ' -f1)"

# Stage the loader rewrite beside the real file. Both the schemas and the three
# consts that describe them are swapped in together at the end, so a failure
# here cannot leave an updated mirror with a stale digest recorded against it.
cp "$dst/loader.go" "$dst/loader.go.tmp"

python3 - "$dst/loader.go.tmp" "$version" "$ref" "$schemas_sha" <<'PY'
import re, sys
path, version, ref, digest = sys.argv[1:5]
src = open(path).read()
# Matches both the grouped form (`\tVersion = "…"`, gofmt-aligned) and a
# standalone `const Version = "…"`, so the rewrite survives either layout.
for name, value in (("Version", version), ("SourceTag", ref), ("SchemasSHA256", digest)):
    pattern = rf'^(?P<prefix>[ \t]*(?:const[ \t]+)?{name}[ \t]*=[ \t]*)"[^"]*"$'
    src, n = re.subn(pattern, lambda m: m.group("prefix") + f'"{value}"', src, count=1, flags=re.M)
    if n != 1:
        sys.exit(f"could not rewrite `{name}` in {path} (matched {n} times, expected 1)")
open(path, "w").write(src)
PY

# gofmt re-aligns the const block after a value changes width; skipping it would
# leave a diff the fmt-check gate rejects.
command -v gofmt >/dev/null && gofmt -w "$dst/loader.go.tmp"

# Commit both halves together.
rm -rf "${dst}/schemas"
mv "${dst}/schemas.tmp" "${dst}/schemas"
mv "$dst/loader.go.tmp" "$dst/loader.go"

echo "synced $ref:$schemas_path -> internal/corestamp/schemas"
echo "  stamp version : $version"
echo "  source tag    : $ref ($(git -C "$core" rev-parse --short "$sha"))"
echo "  schemas sha256: ${schemas_sha:0:16}…"
echo "run 'mise run ci' and commit internal/corestamp/"
