package cli

import (
	"github.com/spf13/cobra"
)

var cdnCmd = &cobra.Command{
	Use:   "cdn",
	Short: "CDN operations",
	Long: `Manage CDN assets (S3 sync).

Examples:
  lw cdn push                   # Push static assets to CDN
  lw cdn pull media             # Pull media from CDN
  lw cdn push media             # Push media to CDN`,
}

var cdnPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push static assets to CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-push")
	},
}

var cdnPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull assets from CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cdnPullMediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Pull media files from CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-pull-media")
	},
}

var cdnPushMediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Push media files to CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-push-media")
	},
}

func init() {
	cdnPullCmd.AddCommand(cdnPullMediaCmd)
	cdnPushCmd.AddCommand(cdnPushMediaCmd)

	cdnCmd.AddCommand(cdnPushCmd)
	cdnCmd.AddCommand(cdnPullCmd)
}
