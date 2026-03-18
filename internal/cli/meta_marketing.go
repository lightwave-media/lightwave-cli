package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	mktAccountID  string
	mktCampaignID string
	mktAdSetID    string
	mktDatePreset string
	mktObjectID   string
)

func resolveAdAccountID() (string, error) {
	if mktAccountID != "" {
		return mktAccountID, nil
	}
	if v := os.Getenv("META_AD_ACCOUNT_ID"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no account ID: set --account-id or META_AD_ACCOUNT_ID")
}

// --- Parent Command ---

var metaMarketingCmd = &cobra.Command{
	Use:   "marketing",
	Short: "Meta Marketing API (ad accounts, campaigns, insights)",
}

// --- Subcommands ---

var mktAccountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "List ad accounts",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		accounts, err := client.ListAdAccounts(ctx)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, accounts)
		}

		if len(accounts) == 0 {
			fmt.Println(color.YellowString("No ad accounts found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Status", "Currency", "Timezone", "Spent", "Balance"})
		table.SetBorder(false)

		for _, a := range accounts {
			status := fmt.Sprintf("%d", a.AccountStatus)
			table.Append([]string{
				a.ID, a.Name, status, a.Currency, a.Timezone, a.AmountSpent, a.Balance,
			})
		}

		table.Render()
		return nil
	},
}

var mktCampaignsCmd = &cobra.Command{
	Use:   "campaigns",
	Short: "List campaigns for an ad account",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		accountID, err := resolveAdAccountID()
		if err != nil {
			return err
		}

		campaigns, err := client.ListCampaigns(ctx, accountID)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, campaigns)
		}

		if len(campaigns) == 0 {
			fmt.Println(color.YellowString("No campaigns found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Status", "Objective", "Daily Budget", "Lifetime Budget"})
		table.SetBorder(false)

		for _, c := range campaigns {
			table.Append([]string{
				c.ID, c.Name, c.Status, c.Objective, c.DailyBudget, c.LifetimeBudget,
			})
		}

		table.Render()
		return nil
	},
}

var mktAdSetsCmd = &cobra.Command{
	Use:   "adsets",
	Short: "List ad sets for an ad account",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		accountID, err := resolveAdAccountID()
		if err != nil {
			return err
		}

		adSets, err := client.ListAdSets(ctx, accountID, mktCampaignID)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, adSets)
		}

		if len(adSets) == 0 {
			fmt.Println(color.YellowString("No ad sets found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Status", "Campaign ID", "Daily Budget", "Bid Amount"})
		table.SetBorder(false)

		for _, s := range adSets {
			table.Append([]string{
				s.ID, s.Name, s.Status, s.CampaignID, s.DailyBudget, s.BidAmount,
			})
		}

		table.Render()
		return nil
	},
}

var mktAdsCmd = &cobra.Command{
	Use:   "ads",
	Short: "List ads for an ad account",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		accountID, err := resolveAdAccountID()
		if err != nil {
			return err
		}

		ads, err := client.ListAds(ctx, accountID, mktAdSetID)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, ads)
		}

		if len(ads) == 0 {
			fmt.Println(color.YellowString("No ads found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Status", "Ad Set ID", "Creative ID", "Creative Name"})
		table.SetBorder(false)

		for _, a := range ads {
			table.Append([]string{
				a.ID, a.Name, a.Status, a.AdSetID, a.Creative.ID, a.Creative.Name,
			})
		}

		table.Render()
		return nil
	},
}

var mktInsightsCmd = &cobra.Command{
	Use:   "insights",
	Short: "Get insights for any object (account, campaign, ad set, ad)",
	Long: `Get performance insights for any Meta Marketing object.

Examples:
  lw meta marketing insights --id act_XXX
  lw meta marketing insights --id <campaign_id> --date-preset last_30d`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		if mktObjectID == "" {
			return fmt.Errorf("--id is required")
		}

		rows, err := client.GetInsights(ctx, mktObjectID, mktDatePreset)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, rows)
		}

		if len(rows) == 0 {
			fmt.Println(color.YellowString("No insights data"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Date Start", "Date Stop", "Impressions", "Clicks", "Spend", "CTR", "CPC", "CPM", "Reach"})
		table.SetBorder(false)

		for _, r := range rows {
			table.Append([]string{
				r.DateStart, r.DateStop, r.Impressions, r.Clicks, r.Spend, r.CTR, r.CPC, r.CPM, r.Reach,
			})
		}

		table.Render()
		return nil
	},
}

func init() {
	// Persistent flag on marketing parent
	metaMarketingCmd.PersistentFlags().StringVar(&mktAccountID, "account-id", "", "Ad account ID (default: META_AD_ACCOUNT_ID env)")

	// campaign-id filter for adsets
	mktAdSetsCmd.Flags().StringVar(&mktCampaignID, "campaign-id", "", "Filter ad sets by campaign ID")

	// adset-id filter for ads
	mktAdsCmd.Flags().StringVar(&mktAdSetID, "adset-id", "", "Filter ads by ad set ID")

	// insights flags
	mktInsightsCmd.Flags().StringVar(&mktObjectID, "id", "", "Object ID to get insights for (required)")
	mktInsightsCmd.Flags().StringVar(&mktDatePreset, "date-preset", "last_7d", "Date preset: today, yesterday, last_7d, last_30d, this_month")
	_ = mktInsightsCmd.MarkFlagRequired("id")

	// Wire subcommands
	metaMarketingCmd.AddCommand(mktAccountsCmd)
	metaMarketingCmd.AddCommand(mktCampaignsCmd)
	metaMarketingCmd.AddCommand(mktAdSetsCmd)
	metaMarketingCmd.AddCommand(mktAdsCmd)
	metaMarketingCmd.AddCommand(mktInsightsCmd)
}
