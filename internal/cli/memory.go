package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/memory"
	"github.com/spf13/cobra"
)

// `lw memory` — Phase 3 / EB-001 plan §3.
//
// Filesystem KV at `~/.lightwave/memory/<namespace>/<key>` for v_core's
// persisted state. Namespaces are agent user-ids (e.g. `v_core`, `cpe`,
// `cqa`); keys may be hierarchical via `/`. Values are raw bytes — the
// caller decides encoding.
//
// New top-level domain, not yet in the lightwave-core schema. Wired
// hardcoded in root.go alongside auditCmd / cdnCmd / agentCmd; schema
// entry lands in a follow-up.

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "v_core persisted state (filesystem KV at ~/.lightwave/memory/)",
	Long: `Get / put / list values in v_core's persistent state store.

Storage: ~/.lightwave/memory/<namespace>/<key>. Namespaces are agent
user-ids; keys may be hierarchical with /. Values are raw bytes (caller
chooses encoding).

Examples:
  lw memory put --namespace v_core --key sprint --value SPR-001
  lw memory put --namespace cpe    --key tasks/T-0001/notes --value-file ./notes.md
  lw memory get --namespace v_core --key sprint
  lw memory list --namespace v_core
  lw memory list                                 # list every namespace`,
}

var (
	memNamespace string
	memKey       string
	memValue     string
	memValueFile string
	memJSON      bool
)

var memoryPutCmd = &cobra.Command{
	Use:          "put",
	Short:        "Write a value at (namespace, key)",
	SilenceUsage: true,
	RunE:         runMemoryPut,
}

var memoryGetCmd = &cobra.Command{
	Use:          "get",
	Short:        "Read a value at (namespace, key) to stdout",
	SilenceUsage: true,
	RunE:         runMemoryGet,
}

var memoryListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List keys in a namespace (or every namespace when omitted)",
	SilenceUsage: true,
	RunE:         runMemoryList,
}

var memoryDeleteCmd = &cobra.Command{
	Use:          "delete",
	Short:        "Remove an entry (idempotent)",
	SilenceUsage: true,
	RunE:         runMemoryDelete,
}

func init() {
	for _, c := range []*cobra.Command{memoryPutCmd, memoryGetCmd, memoryListCmd, memoryDeleteCmd} {
		c.Flags().StringVar(&memNamespace, "namespace", "", "Namespace (agent user-id)")
	}

	for _, c := range []*cobra.Command{memoryPutCmd, memoryGetCmd, memoryDeleteCmd} {
		c.Flags().StringVar(&memKey, "key", "", "Key (may contain / for hierarchy)")
		_ = c.MarkFlagRequired("key")
		_ = c.MarkFlagRequired("namespace")
	}

	memoryPutCmd.Flags().StringVar(&memValue, "value", "", "Inline value (use --value-file for binary / large values)")
	memoryPutCmd.Flags().StringVar(&memValueFile, "value-file", "", "Read value from file (use - for stdin)")
	memoryListCmd.Flags().BoolVar(&memJSON, "json", false, "Emit JSON array of keys (or namespaces)")
	memoryGetCmd.Flags().BoolVar(&memJSON, "json", false, "Emit JSON envelope {namespace, key, value_b64}")

	memoryCmd.AddCommand(memoryPutCmd)
	memoryCmd.AddCommand(memoryGetCmd)
	memoryCmd.AddCommand(memoryListCmd)
	memoryCmd.AddCommand(memoryDeleteCmd)
}

func runMemoryPut(_ *cobra.Command, _ []string) error {
	var value []byte

	switch {
	case memValueFile == "" && memValue == "":
		return errors.New("supply --value or --value-file")
	case memValueFile != "" && memValue != "":
		return errors.New("--value and --value-file are mutually exclusive")
	case memValueFile == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}

		value = b
	case memValueFile != "":
		b, err := os.ReadFile(memValueFile)
		if err != nil {
			return fmt.Errorf("read %s: %w", memValueFile, err)
		}

		value = b
	default:
		value = []byte(memValue)
	}

	path, err := memory.Put(memNamespace, memKey, value)
	if err != nil {
		return err
	}

	fmt.Printf("wrote %s/%s (%d bytes) -> %s\n",
		color.CyanString(memNamespace), color.YellowString(memKey),
		len(value), path)

	return nil
}

func runMemoryGet(_ *cobra.Command, _ []string) error {
	value, err := memory.Get(memNamespace, memKey)
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return err
	}

	if memJSON {
		return emitJSON(map[string]any{
			"namespace": memNamespace,
			"key":       memKey,
			"value":     string(value),
			"size":      len(value),
		})
	}

	_, err = os.Stdout.Write(value)

	return err
}

func runMemoryList(_ *cobra.Command, _ []string) error {
	if memNamespace == "" {
		nss, err := memory.Namespaces()
		if err != nil {
			return err
		}

		if memJSON {
			return emitJSON(nss)
		}

		if len(nss) == 0 {
			fmt.Println(color.YellowString("No namespaces written yet."))
			return nil
		}

		for _, ns := range nss {
			fmt.Println(ns)
		}

		return nil
	}

	keys, err := memory.List(memNamespace)
	if err != nil {
		return err
	}

	if memJSON {
		return emitJSON(keys)
	}

	if len(keys) == 0 {
		fmt.Println(color.YellowString("(empty)"))
		return nil
	}

	for _, k := range keys {
		fmt.Println(k)
	}

	return nil
}

func runMemoryDelete(_ *cobra.Command, _ []string) error {
	if err := memory.Delete(memNamespace, memKey); err != nil {
		return err
	}

	fmt.Printf("deleted %s/%s\n",
		color.CyanString(memNamespace), color.YellowString(memKey))

	return nil
}
