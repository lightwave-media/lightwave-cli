package cli

import (
	"context"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/githuborg"
)

func init() {
	RegisterHandler("scrum.sync", scrumSyncHandler)
}

func scrumSyncHandler(ctx context.Context, _ []string, flags map[string]any) error {
	opts := githuborg.Options{
		Org:           flagStr(flags, "org"),
		LightwaveRoot: lightwaveRoot(),
		TargetRepo:    flagStr(flags, "repo"),
		FullOrg:       flagBool(flags, "full-org"),
		DryRun:        flagBool(flags, "dry-run"),
	}

	report, err := githuborg.Sync(ctx, opts)
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(report)
	}

	fmt.Printf("scrum sync: org=%s project=%d bootstrap=%v added=%d skipped=%d errors=%d\n",
		report.Org, report.ProjectNumber, report.BootstrapRan,
		report.IssuesAdded, report.IssuesSkipped, len(report.Errors))

	for _, e := range report.Errors {
		fmt.Printf("  error: %s\n", e)
	}

	return nil
}
