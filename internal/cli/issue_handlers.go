package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fatih/color"
	gh "github.com/lightwave-media/lightwave-cli/internal/github"
)

func init() {
	RegisterHandler("issue.create", issueCreateHandler)
}

func issueCreateHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("title required: lw issue create <title> with --kind and --motivation")
	}

	kind := gh.IssueKind(flagStr(flags, "kind"))
	if kind == "" {
		kind = gh.KindFeatureRequest
	}

	switch kind {
	case gh.KindFeatureRequest, gh.KindBugReport, gh.KindToolGap:
	default:
		return fmt.Errorf("invalid --kind %q (want feature_request, bug_report, or tool_gap)", kind)
	}

	projectNum := gh.DefaultProjectNum

	if ps := flagStr(flags, "project"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid --project %q", ps)
		}

		projectNum = n
	}

	opts := gh.IssueCreateOpts{
		Repo:           flagStrOr(flags, "repo", gh.DefaultRepo),
		Title:          args[0],
		Kind:           kind,
		Motivation:     flagStr(flags, "motivation"),
		ProposedChange: flagStr(flags, "proposed-change"),
		Scope:          flagStr(flags, "scope"),
		KindDetail:     flagStr(flags, "kind-detail"),
		Labels:         flagStrSlice(flags, "label"),
		Refs:           flagStrSlice(flags, "refs"),
		Closes:         flagStrSlice(flags, "closes"),
		Origin:         flagStr(flags, "origin"),
		Milestone:      flagStr(flags, "milestone"),
		ProjectNumber:  projectNum,
		Org:            flagStrOr(flags, "org", gh.DefaultIssueOrg),
		DryRun:         flagBool(flags, "dry-run"),
	}

	result, err := gh.CreateCompliantIssue(opts)
	if err != nil {
		return err
	}

	if opts.DryRun {
		return nil
	}

	fmt.Printf("%s Issue #%d\n", color.GreenString("Created"), result.Number)
	fmt.Println(result.URL)

	return nil
}

func flagStrSlice(flags map[string]any, key string) []string {
	v, ok := flags[key]
	if !ok {
		return nil
	}

	switch t := v.(type) {
	case []string:
		return t
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}

		return strings.Split(t, ",")
	default:
		return nil
	}
}
