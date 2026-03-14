package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var emailCmd = &cobra.Command{
	Use:   "email",
	Short: "Email operations",
}

var (
	emailTemplate string
	emailTo       string
	emailSubject  string
	emailProps    string
	emailFrom     string
	emailTenant   string
	emailDryRun   bool
)

var emailSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send an email using a React Email template",
	Long: `Send an email using a React Email template via the Django backend.

Examples:
  lw email send --template EmailConfirmation --to user@example.com --subject "Welcome" --props '{"userName":"Joel"}'
  lw email send --template EmailConfirmation --to user@example.com --subject "Welcome" --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		// Build the management command string
		var parts []string
		parts = append(parts, "send_email")
		parts = append(parts, fmt.Sprintf("--template %s", emailTemplate))
		parts = append(parts, fmt.Sprintf("--to %s", emailTo))
		parts = append(parts, fmt.Sprintf("--subject %q", emailSubject))

		if emailProps != "" {
			parts = append(parts, fmt.Sprintf("--props %s", emailProps))
		}
		if emailFrom != "" {
			parts = append(parts, fmt.Sprintf("--from %s", emailFrom))
		}
		if emailTenant != "" {
			parts = append(parts, fmt.Sprintf("--tenant %s", emailTenant))
		}
		if emailDryRun {
			parts = append(parts, "--dry-run")
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}

func init() {
	emailSendCmd.Flags().StringVar(&emailTemplate, "template", "", "React Email component name (required)")
	emailSendCmd.Flags().StringVar(&emailTo, "to", "", "Recipient email address (required)")
	emailSendCmd.Flags().StringVar(&emailSubject, "subject", "", "Email subject line (required)")
	emailSendCmd.Flags().StringVar(&emailProps, "props", "", "JSON props for the template")
	emailSendCmd.Flags().StringVar(&emailFrom, "from", "", "Sender email address override")
	emailSendCmd.Flags().StringVar(&emailTenant, "tenant", "", "Tenant schema_name for schema_context (e.g. lightwave_media)")
	emailSendCmd.Flags().BoolVar(&emailDryRun, "dry-run", false, "Render only, don't send")

	_ = emailSendCmd.MarkFlagRequired("template")
	_ = emailSendCmd.MarkFlagRequired("to")
	_ = emailSendCmd.MarkFlagRequired("subject")

	emailCmd.AddCommand(emailSendCmd)
}
