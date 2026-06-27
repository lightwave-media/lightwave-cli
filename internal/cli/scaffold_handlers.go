package cli

import (
	"context"
	"errors"
)

// Schema-driven scaffold handlers. commands.yaml v3.0.0 declares 4 commands:
// app, model, api, test. These produce Django/Go skeletons aligned to
// LightWave's app structure conventions.
//
// Handlers are stubbed: scaffold templates live in internal/scaffold/ but
// the schema-driven entrypoint requires --tier/--name/--app/--model/--fields
// flag plumbing into the existing template engine. Until that's wired,
// surface the gap rather than silently no-op.

func init() {
	RegisterHandler("scaffold.app", scaffoldAppHandler)
	RegisterHandler("scaffold.model", scaffoldModelHandler)
	RegisterHandler("scaffold.api", scaffoldApiHandler)
	RegisterHandler("scaffold.test", scaffoldTestHandler)
}

func scaffoldAppHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagStr(flags, "name") == "" || flagStr(flags, "tier") == "" {
		return errors.New("usage: lw scaffold app --name=<n> --tier=<core|platform|integration> [--dry-run]")
	}

	return errors.New("scaffold app: not yet wired (template engine in internal/scaffold/ exists but dispatcher entrypoint pending)")
}

func scaffoldModelHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagStr(flags, "app") == "" || flagStr(flags, "name") == "" {
		return errors.New("usage: lw scaffold model --app=<a> --name=<n> --fields=<f1,f2> [--dry-run]")
	}

	return errors.New("scaffold model: not yet wired (template engine in internal/scaffold/ exists but dispatcher entrypoint pending)")
}

func scaffoldApiHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagStr(flags, "app") == "" || flagStr(flags, "model") == "" {
		return errors.New("usage: lw scaffold api --app=<a> --model=<m> [--dry-run]")
	}

	return errors.New("scaffold api: not yet wired (template engine in internal/scaffold/ exists but dispatcher entrypoint pending)")
}

func scaffoldTestHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagStr(flags, "app") == "" || flagStr(flags, "model") == "" {
		return errors.New("usage: lw scaffold test --app=<a> --model=<m> [--adversarial] [--dry-run]")
	}

	return errors.New("scaffold test: not yet wired (template engine in internal/scaffold/ exists but dispatcher entrypoint pending)")
}
