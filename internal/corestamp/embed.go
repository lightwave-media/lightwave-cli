package corestamp

import "embed"

// schemaFS is a vendored mirror of lightwave-core's src/schemas, copied in from
// that repo's bindings/go module.
//
// It is vendored rather than imported because lightwave-cli is a PUBLIC repo and
// lightwave-core is private: a module dependency makes `go build` and `go install`
// fail for anyone outside the org, and needs a private-module token in every CI
// job. A committed copy keeps the public build working with no credentials.
//
// Refresh it with scripts/sync-core-stamp.sh after any change to the stamp; that
// script is the single supported way to update this tree by hand.
//
// The `all:` prefix is required so __index.yaml (leading underscore) is
// embedded — go:embed excludes _/. files otherwise.
//
//go:embed all:schemas
var schemaFS embed.FS
