package github

import "github.com/lightwave-media/lightwave-cli/internal/git"

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
