package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "Document management",
	Long:  `Create and manage createOS documents (PRD, SAD, NFR, DDD, etc.) with lineage tracking.`,
}

var docCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a document linked to an epic or story",
	Long: `Create a new document in the database with lineage tracking.

The document is created as a draft with the specified category and linked
to the given epic or user story via foreign key.

Examples:
  lw doc create --category prd --epic b902c1b4 --title "Platform PRD"
  lw doc create --category sad --epic b902c1b4 --title "Platform SAD"
  lw doc create --category ddd --story 81a1e5be --title "Auth DDD"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		category, _ := cmd.Flags().GetString("category")
		epicID, _ := cmd.Flags().GetString("epic")
		storyID, _ := cmd.Flags().GetString("story")
		title, _ := cmd.Flags().GetString("title")

		if category == "" {
			return fmt.Errorf("--category is required (prd, sad, nfr, ddd, api_spec, product_vision, market_analysis)")
		}
		if title == "" {
			return fmt.Errorf("--title is required")
		}
		if epicID == "" && storyID == "" {
			return fmt.Errorf("--epic or --story is required for lineage tracking")
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		doc, err := db.CreateDocument(ctx, pool, db.DocumentCreateOptions{
			Category:    category,
			Title:       title,
			EpicID:      epicID,
			UserStoryID: storyID,
		})
		if err != nil {
			return err
		}

		fmt.Printf("%s Created %s document: %s\n",
			color.GreenString("✓"),
			color.CyanString(doc.Category),
			color.YellowString(doc.ShortID))
		fmt.Printf("  Title: %s\n", doc.Title)
		if doc.EpicID != nil {
			eid := *doc.EpicID
			if len(eid) > 8 {
				eid = eid[:8]
			}
			fmt.Printf("  Epic:  %s\n", eid)
		}
		if doc.UserStoryID != nil {
			sid := *doc.UserStoryID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			fmt.Printf("  Story: %s\n", sid)
		}
		return nil
	},
}

var docListCmd = &cobra.Command{
	Use:   "list",
	Short: "List documents",
	Long: `List documents, optionally filtered by category or epic.

Examples:
  lw doc list
  lw doc list --category prd
  lw doc list --epic b902c1b4`,
	RunE: func(cmd *cobra.Command, args []string) error {
		category, _ := cmd.Flags().GetString("category")
		epicID, _ := cmd.Flags().GetString("epic")

		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		docs, err := db.ListDocuments(ctx, pool, category, epicID)
		if err != nil {
			return err
		}

		if len(docs) == 0 {
			fmt.Println(color.YellowString("No documents found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Category", "Title", "Status", "Linked To"})
		table.SetBorder(false)
		table.SetHeaderColor(
			tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
			tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
			tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
			tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
			tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		)

		for _, d := range docs {
			linkedTo := ""
			if d.EpicID != nil {
				eid := *d.EpicID
				if len(eid) > 8 {
					eid = eid[:8]
				}
				linkedTo = "epic " + eid
			}
			if d.UserStoryID != nil {
				sid := *d.UserStoryID
				if len(sid) > 8 {
					sid = sid[:8]
				}
				if linkedTo != "" {
					linkedTo += ", "
				}
				linkedTo += "story " + sid
			}

			title := d.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}

			table.Append([]string{
				d.ShortID,
				strings.ToUpper(d.Category),
				title,
				d.Status,
				linkedTo,
			})
		}

		table.Render()
		return nil
	},
}

var docUpdateCmd = &cobra.Command{
	Use:   "update [doc-id]",
	Short: "Update a document's status or title",
	Long: `Update a document by short ID prefix.

Valid statuses: draft, in_review, approved, published, archived, superseded

Examples:
  lw doc update 322973ad --status published
  lw doc update 322973ad --title "Updated PRD Title"
  lw doc update 322973ad --status published --title "Final PRD"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetString("status")
		title, _ := cmd.Flags().GetString("title")

		opts := db.DocumentUpdateOptions{}
		if cmd.Flags().Changed("status") {
			opts.Status = &status
		}
		if cmd.Flags().Changed("title") {
			opts.Title = &title
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		doc, err := db.UpdateDocument(ctx, pool, args[0], opts)
		if err != nil {
			return err
		}

		fmt.Printf("%s Updated %s document %s\n",
			color.GreenString("✓"),
			color.CyanString(strings.ToUpper(doc.Category)),
			color.YellowString(doc.ShortID))
		fmt.Printf("  Status: %s\n", doc.Status)
		fmt.Printf("  Title:  %s\n", doc.Title)
		return nil
	},
}

func init() {
	docCreateCmd.Flags().String("category", "", "Document category (prd, sad, nfr, ddd, api_spec, product_vision, market_analysis)")
	docCreateCmd.Flags().String("epic", "", "Epic ID to link document to")
	docCreateCmd.Flags().String("story", "", "User story ID to link document to")
	docCreateCmd.Flags().String("title", "", "Document title")

	docListCmd.Flags().String("category", "", "Filter by category")
	docListCmd.Flags().String("epic", "", "Filter by epic ID")

	docUpdateCmd.Flags().String("status", "", "New status (draft, in_review, approved, published, archived, superseded)")
	docUpdateCmd.Flags().String("title", "", "New title")

	docCmd.AddCommand(docCreateCmd)
	docCmd.AddCommand(docListCmd)
	docCmd.AddCommand(docUpdateCmd)
}
