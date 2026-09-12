package github

import (
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/git"
)

// QualifyRepo turns a bare repository name into the "OWNER/REPO" slug gh
// requires, using org as the owner. An already-qualified slug is returned
// unchanged, as is an empty one.
//
// gh accepts only "[HOST/]OWNER/REPO". Passing --repo through verbatim meant
// the natural invocation the conventions ask for —
//
//	lw issue create '<title>' --kind tool_gap --repo lightwave-cli --org lightwave-media
//
// failed with `expected the "[HOST/]OWNER/REPO"`, because --org was collected
// and then never composed into the slug (#372). Every caller that wanted a
// different repo had to know to pre-qualify it, and --dry-run printed the
// unqualified name, so the flag combination looked correct right up until the
// real run.
//
// A host-qualified value (host/owner/repo) already contains a slash and passes
// through untouched, which is the behaviour gh wants.
func QualifyRepo(repo, org string) string {
	if repo == "" || org == "" || strings.Contains(repo, "/") {
		return repo
	}

	return org + "/" + repo
}

// CurrentRepo returns the "owner/repo" slug of the repository containing dir,
// derived from its origin remote. Pass "" for the working directory.
//
// It falls back to PlatformRepo only when there is no repository and no origin
// to read — outside a checkout there is no "here" to file against, and the
// agile-artifact home is the least surprising destination.
//
// Use this for anything that should act on the repo the operator is standing
// in: filing an issue, recording a failure, looking up a PR. Reach for
// PlatformRepo directly only when the target really is the agile-artifact
// store regardless of location.
func CurrentRepo(dir string) string {
	if slug := git.NewGit(dir).OriginSlug(); slug != "" {
		return slug
	}

	return PlatformRepo
}
