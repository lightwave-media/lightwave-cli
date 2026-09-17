package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/knowledge"
)

const knowledgeDatabaseTimeout = 5 * time.Second

func init() {
	RegisterHandler("knowledge.sync", knowledgeSyncHandler)
	RegisterHandler("knowledge.status", knowledgeStatusHandler)
	RegisterHandler("knowledge.reindex", knowledgeReindexHandler)
	RegisterHandler("knowledge.migrate", knowledgeMigrateHandler)
}

func knowledgeSyncHandler(ctx context.Context, _ []string, flags map[string]any) error {
	token, err := knowledge.RuntimeToken(ctx)
	if err != nil {
		return err
	}

	remote, err := knowledge.NewClient(token)
	if err != nil {
		return err
	}

	engine := knowledge.Engine{Files: knowledge.Files{Root: config.PrintRoot()}, Remote: remote}

	dryRun := flagBool(flags, "dry-run")
	if !dryRun {
		pool, err := knowledgePool(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()

		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass('public.external_refs') IS NOT NULL AND to_regclass('public.notion_pages') IS NOT NULL").Scan(&exists); err != nil {
			return err
		}

		if !exists {
			return errors.New("notion projection tables are missing; review lw knowledge migrate --dry-run before applying the migration")
		}

		engine.Projector = knowledge.Postgres{Pool: pool}
	}
	// Every run enumerates sources; --full also refreshes unchanged page bodies.
	report, runErr := engine.Run(ctx, knowledge.Options{Database: flagStr(flags, "database"), DryRun: dryRun, Full: flagBool(flags, "full")})
	if err := printKnowledge(report, flags); err != nil {
		return err
	}

	if runErr != nil {
		return runErr
	}

	for _, change := range report.Changes {
		if change.Action == knowledge.StatusDrift {
			return errors.New("notion reconciliation has unresolved competing edits; both versions were retained")
		}
	}

	return nil
}

func knowledgeStatusHandler(_ context.Context, _ []string, flags map[string]any) error {
	entries, err := knowledge.Status(knowledge.Files{Root: config.PrintRoot()})
	if err != nil {
		return err
	}

	return printKnowledge(entries, flags)
}

func knowledgeMigrateHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if flagBool(flags, "dry-run") {
		sql, err := knowledge.MigrationSQL()
		if err != nil {
			return err
		}

		if flagBool(flags, "json") {
			return printKnowledge(map[string]string{"sql": sql}, flags)
		}

		fmt.Print(sql)

		return nil
	}

	if !flagBool(flags, "yes") {
		return errors.New("review lw knowledge migrate --dry-run, then pass --yes to apply to the configured database")
	}

	pool, err := knowledgePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := knowledge.Migrate(ctx, pool); err != nil {
		return err
	}

	return printKnowledge(struct {
		Status string `json:"status"`
		Tables string `json:"tables"`
	}{Status: "applied", Tables: "notion_databases, notion_pages, external_refs"}, flags)
}

func knowledgePool(ctx context.Context) (*pgxpool.Pool, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("CLI configuration is not loaded")
	}

	ctx, cancel := context.WithTimeout(ctx, knowledgeDatabaseTimeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.GetDSN())
	if err != nil {
		return nil, errors.New("invalid local database configuration")
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("local projection database is unavailable")
	}

	return pool, nil
}

func printKnowledge(value any, _ map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(data))

	return nil
}

func knowledgeReindexHandler(ctx context.Context, _ []string, flags map[string]any) error {
	dryRun := flagBool(flags, "dry-run")

	var projector knowledge.Projector

	if !dryRun {
		pool, err := knowledgePool(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()

		projector = knowledge.Postgres{Pool: pool}
	}

	report, runErr := knowledge.Reindex(ctx, knowledge.Files{Root: config.PrintRoot()}, projector, dryRun)
	if err := printKnowledge(report, flags); err != nil {
		return err
	}

	return runErr
}
