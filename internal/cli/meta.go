package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/meta"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

const (
	defaultWABAID = "1924462211484044"
	defaultAppID  = "1328704275761432"
)

var (
	metaToken     string
	metaAppSecret string
	metaWABAID    string
	metaAppID     string
	metaOutput    string
)

// resolveMetaToken returns the token from flag or env.
func resolveMetaToken() (string, error) {
	if metaToken != "" {
		return metaToken, nil
	}
	if v := os.Getenv("WHATSAPP_ACCESS_TOKEN"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no token: set --token or WHATSAPP_ACCESS_TOKEN")
}

func resolveWABAID() string {
	if metaWABAID != "" {
		return metaWABAID
	}
	if v := os.Getenv("META_WABA_ID"); v != "" {
		return v
	}
	return defaultWABAID
}

func resolveAppID() string {
	if metaAppID != "" {
		return metaAppID
	}
	if v := os.Getenv("META_APP_ID"); v != "" {
		return v
	}
	return defaultAppID
}

func resolveAppSecret() string {
	if metaAppSecret != "" {
		return metaAppSecret
	}
	return os.Getenv("META_APP_SECRET")
}

func newMetaClient() (*meta.Client, error) {
	token, err := resolveMetaToken()
	if err != nil {
		return nil, err
	}
	return meta.NewClient(token, resolveAppSecret()), nil
}

func newMetaAppClient() (*meta.Client, error) {
	secret := resolveAppSecret()
	if secret == "" {
		return nil, fmt.Errorf("no app secret: set --app-secret or META_APP_SECRET")
	}
	return meta.NewAppClient(resolveAppID(), secret), nil
}

// --- Parent Commands ---

var metaCmd = &cobra.Command{
	Use:   "meta",
	Short: "Meta Graph API operations (WhatsApp, tokens)",
	Long:  `Manage Meta Graph API resources - WhatsApp phone numbers, webhooks, tokens.`,
}

var metaWhatsAppCmd = &cobra.Command{
	Use:   "whatsapp",
	Short: "WhatsApp Business API operations",
}

var metaTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Token inspection and debugging",
}

// --- WhatsApp Commands ---

var metaNumbersCmd = &cobra.Command{
	Use:   "numbers",
	Short: "List phone numbers on WABA",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		numbers, err := client.ListPhoneNumbers(ctx, resolveWABAID())
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, numbers)
		}

		if len(numbers) == 0 {
			fmt.Println(color.YellowString("No phone numbers found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Number", "Name", "Quality", "Status"})
		table.SetBorder(false)

		for _, n := range numbers {
			table.Append([]string{
				n.ID,
				n.DisplayPhoneNumber,
				n.VerifiedName,
				n.QualityRating,
				n.CodeVerificationStatus,
			})
		}

		table.Render()
		return nil
	},
}

var (
	numberAddCC     string
	numberAddNumber string
)

var metaNumberAddCmd = &cobra.Command{
	Use:   "number-add",
	Short: "Add a phone number to WABA",
	Long: `Add a phone number to the WhatsApp Business Account.

Examples:
  lw meta whatsapp number-add --cc 1 --number 2125551234`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		resp, err := client.AddPhoneNumber(ctx, resolveWABAID(), meta.AddPhoneNumberOpts{
			CountryCode: numberAddCC,
			PhoneNumber: numberAddNumber,
		})
		if err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Phone number added"))
		fmt.Println(string(resp))
		return nil
	},
}

var (
	registerPhoneID string
	registerPin     string
)

var metaRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register phone for WhatsApp messaging",
	Long: `Register a phone number so it can send and receive WhatsApp messages.

Examples:
  lw meta whatsapp register --phone-id 123456 --pin 123456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		_, err = client.RegisterPhone(ctx, registerPhoneID, registerPin)
		if err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Phone registered"))
		return nil
	},
}

var deregisterPhoneID string

var metaDeregisterCmd = &cobra.Command{
	Use:   "deregister",
	Short: "Deregister phone from WhatsApp messaging",
	Long: `Deregister a phone number from WhatsApp messaging.

Examples:
  lw meta whatsapp deregister --phone-id 123456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		_, err = client.DeregisterPhone(ctx, deregisterPhoneID)
		if err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Phone deregistered"))
		return nil
	},
}

var metaWebhookGetCmd = &cobra.Command{
	Use:   "webhook-get",
	Short: "Show webhook subscriptions",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaAppClient()
		if err != nil {
			return err
		}

		subs, err := client.GetWebhookSubscriptions(ctx, resolveAppID())
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, subs)
		}

		if len(subs) == 0 {
			fmt.Println(color.YellowString("No webhook subscriptions"))
			return nil
		}

		for _, s := range subs {
			activeStr := color.RedString("inactive")
			if s.Active {
				activeStr = color.GreenString("active")
			}

			var fieldNames []string
			for _, f := range s.Fields {
				fieldNames = append(fieldNames, f.Name)
			}

			fmt.Printf("%s  %s  %s\n", color.CyanString(s.Object), activeStr, s.CallbackURL)
			if len(fieldNames) > 0 {
				fmt.Printf("  fields: %s\n", strings.Join(fieldNames, ", "))
			}
		}

		return nil
	},
}

var (
	webhookURL         string
	webhookVerifyToken string
	webhookFields      string
)

var metaWebhookSetCmd = &cobra.Command{
	Use:   "webhook-set",
	Short: "Configure webhook subscription",
	Long: `Configure a webhook subscription for WhatsApp on the Meta app.

Examples:
  lw meta whatsapp webhook-set --url https://example.com/webhook --verify-token mytoken --fields messages`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaAppClient()
		if err != nil {
			return err
		}

		fields := strings.Split(webhookFields, ",")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}

		_, err = client.SetWebhook(ctx, resolveAppID(), webhookURL, webhookVerifyToken, fields)
		if err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Webhook configured"))
		fmt.Printf("  url:    %s\n", webhookURL)
		fmt.Printf("  fields: %s\n", strings.Join(fields, ", "))
		return nil
	},
}

var (
	sendTo      string
	sendMessage string
)

var metaSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a test text message",
	Long: `Send a text message via the WhatsApp Cloud API.

Uses the WHATSAPP_PHONE_NUMBER_ID env var as the sender phone.

Examples:
  lw meta whatsapp send --to +12125551234 --message "Hello from LightWave"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client, err := newMetaClient()
		if err != nil {
			return err
		}

		phoneID := os.Getenv("WHATSAPP_PHONE_NUMBER_ID")
		if phoneID == "" {
			return fmt.Errorf("WHATSAPP_PHONE_NUMBER_ID not set")
		}

		resp, err := client.SendTextMessage(ctx, phoneID, sendTo, sendMessage)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, resp)
		}

		fmt.Println(color.GreenString("✓ Message sent"))
		if len(resp.Messages) > 0 {
			fmt.Printf("  message_id: %s\n", resp.Messages[0].ID)
		}
		return nil
	},
}

// --- Token Commands ---

var metaTokenCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate token and show permissions/expiry",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		token, err := resolveMetaToken()
		if err != nil {
			return err
		}
		client := meta.NewClient(token, resolveAppSecret())

		info, err := client.DebugToken(ctx, token)
		if err != nil {
			return err
		}

		outputJSON, err := useJSONOutput(metaOutput)
		if err != nil {
			return err
		}
		if outputJSON {
			return writeJSON(os.Stdout, info)
		}

		validStr := color.RedString("✗ invalid")
		if info.IsValid {
			validStr = color.GreenString("✓ valid")
		}

		expiryStr := "never"
		if info.ExpiresAt != 0 {
			expiryStr = time.Unix(info.ExpiresAt, 0).Format(time.RFC3339)
		}

		fmt.Printf("%s  %s\n", color.CyanString("Status:"), validStr)
		fmt.Printf("%s  %s\n", color.CyanString("Type:"), info.Type)
		fmt.Printf("%s  %s\n", color.CyanString("App:"), info.Application)
		fmt.Printf("%s  %s\n", color.CyanString("Expires:"), expiryStr)

		if len(info.Scopes) > 0 {
			fmt.Printf("%s  %s\n", color.CyanString("Scopes:"), strings.Join(info.Scopes, ", "))
		}

		if len(info.GranularScopes) > 0 {
			fmt.Printf("%s\n", color.CyanString("Granular Scopes:"))
			for _, gs := range info.GranularScopes {
				targets := ""
				if len(gs.TargetIDs) > 0 {
					targets = " → " + strings.Join(gs.TargetIDs, ", ")
				}
				fmt.Printf("  %s%s\n", gs.Scope, targets)
			}
		}

		return nil
	},
}

var metaTokenDebugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Raw debug_token JSON output",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		token, err := resolveMetaToken()
		if err != nil {
			return err
		}
		client := meta.NewClient(token, resolveAppSecret())

		info, err := client.DebugToken(ctx, token)
		if err != nil {
			return err
		}

		return writeJSON(os.Stdout, info)
	},
}

// --- Doctor Command ---

var (
	doctorSendTest bool
	doctorSendTo   string
)

var metaDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run all Meta API health checks",
	Long: `Run health checks against the Meta Graph API:
- Token validity and permissions
- Phone number registration status
- Webhook configuration

Examples:
  lw meta doctor
  lw meta doctor --send-test --to +12125551234`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		allPassed := true

		// 1. Environment prerequisites — check all required vars
		fmt.Println(color.CyanString("Prerequisites"))

		tokenSource := "not found"
		token := ""
		if metaToken != "" {
			token = metaToken
			tokenSource = "--token flag"
		} else if v := os.Getenv("WHATSAPP_ACCESS_TOKEN"); v != "" {
			token = v
			tokenSource = "WHATSAPP_ACCESS_TOKEN env"
		}
		if token == "" {
			fmt.Printf("  %-20s %s set --token or WHATSAPP_ACCESS_TOKEN\n", "Access Token", color.RedString("✗"))
			allPassed = false
		} else {
			fmt.Printf("  %-20s %s (%s)\n", "Access Token", color.GreenString("✓"), tokenSource)
		}

		wabaID := resolveWABAID()
		wabaSource := "default"
		if metaWABAID != "" {
			wabaSource = "--waba-id flag"
		} else if os.Getenv("META_WABA_ID") != "" {
			wabaSource = "META_WABA_ID env"
		}
		fmt.Printf("  %-20s %s %s (%s)\n", "WABA ID", color.GreenString("✓"), wabaID, wabaSource)

		appID := resolveAppID()
		appIDSource := "default"
		if metaAppID != "" {
			appIDSource = "--app-id flag"
		} else if os.Getenv("META_APP_ID") != "" {
			appIDSource = "META_APP_ID env"
		}
		fmt.Printf("  %-20s %s %s (%s)\n", "App ID", color.GreenString("✓"), appID, appIDSource)

		appSecret := resolveAppSecret()
		if appSecret == "" {
			fmt.Printf("  %-20s %s set --app-secret or META_APP_SECRET (needed for webhooks)\n", "App Secret", color.YellowString("?"))
		} else {
			fmt.Printf("  %-20s %s set\n", "App Secret", color.GreenString("✓"))
		}

		phoneID := os.Getenv("WHATSAPP_PHONE_NUMBER_ID")
		if phoneID == "" {
			fmt.Printf("  %-20s %s WHATSAPP_PHONE_NUMBER_ID not set (needed for sending)\n", "Phone Number ID", color.YellowString("?"))
		} else {
			fmt.Printf("  %-20s %s %s\n", "Phone Number ID", color.GreenString("✓"), phoneID)
		}

		// Short-circuit if no token
		if token == "" {
			fmt.Println()
			return fmt.Errorf("cannot run API checks without an access token")
		}

		client := meta.NewClient(token, appSecret)
		fmt.Println()
		fmt.Println(color.CyanString("API Checks"))

		// 2. Token validation
		info, err := client.DebugToken(ctx, token)
		if err != nil {
			fmt.Printf("  %-20s %s %s\n", "Token Valid", color.RedString("✗"), err)
			allPassed = false
		} else if !info.IsValid {
			fmt.Printf("  %-20s %s token is invalid or expired\n", "Token Valid", color.RedString("✗"))
			allPassed = false
		} else {
			expiryStr := "never expires"
			if info.ExpiresAt != 0 {
				expiryStr = "expires " + time.Unix(info.ExpiresAt, 0).Format("2006-01-02")
			}
			fmt.Printf("  %-20s %s %s (%s)\n", "Token Valid", color.GreenString("✓"), info.Type, expiryStr)
		}

		// 3. Permissions
		if info != nil && len(info.Scopes) > 0 {
			fmt.Printf("  %-20s %s %s\n", "Permissions", color.GreenString("✓"), strings.Join(info.Scopes, ", "))
		} else if info != nil {
			fmt.Printf("  %-20s %s no scopes found\n", "Permissions", color.YellowString("?"))
		}

		// 4. Phone numbers
		numbers, err := client.ListPhoneNumbers(ctx, wabaID)
		if err != nil {
			fmt.Printf("  %-20s %s %s\n", "Phone Numbers", color.RedString("✗"), err)
			allPassed = false
		} else if len(numbers) == 0 {
			fmt.Printf("  %-20s %s none registered on WABA %s\n", "Phone Numbers", color.YellowString("?"), wabaID)
		} else {
			fmt.Printf("  %-20s %s %d registered\n", "Phone Numbers", color.GreenString("✓"), len(numbers))
		}

		// 5. Webhook (requires app-level auth)
		if appSecret != "" {
			appClient := meta.NewAppClient(appID, appSecret)
			subs, webhookErr := appClient.GetWebhookSubscriptions(ctx, appID)
			if webhookErr != nil {
				fmt.Printf("  %-20s %s %s\n", "Webhook", color.RedString("✗"), webhookErr)
				allPassed = false
			} else {
				found := false
				for _, s := range subs {
					if s.Object == "whatsapp_business_account" && s.Active {
						found = true
						fmt.Printf("  %-20s %s %s\n", "Webhook", color.GreenString("✓"), s.CallbackURL)
						break
					}
				}
				if !found {
					fmt.Printf("  %-20s %s no active whatsapp_business_account subscription\n", "Webhook", color.RedString("✗"))
					allPassed = false
				}
			}
		} else {
			fmt.Printf("  %-20s %s skipped (no app secret)\n", "Webhook", color.YellowString("-"))
		}

		// 6. Optional send test
		if doctorSendTest {
			fmt.Println()
			fmt.Println(color.CyanString("Send Test"))
			if doctorSendTo == "" {
				fmt.Printf("  %-20s %s --to flag required with --send-test\n", "Send Test", color.RedString("✗"))
				allPassed = false
			} else if phoneID == "" {
				fmt.Printf("  %-20s %s WHATSAPP_PHONE_NUMBER_ID not set\n", "Send Test", color.RedString("✗"))
				allPassed = false
			} else {
				_, sendErr := client.SendTextMessage(ctx, phoneID, doctorSendTo, "LightWave doctor test")
				if sendErr != nil {
					fmt.Printf("  %-20s %s %s\n", "Send Test", color.RedString("✗"), sendErr)
					allPassed = false
				} else {
					fmt.Printf("  %-20s %s message sent to %s\n", "Send Test", color.GreenString("✓"), doctorSendTo)
				}
			}
		}

		fmt.Println()
		if !allPassed {
			return fmt.Errorf("some checks failed")
		}
		fmt.Println(color.GreenString("All checks passed"))
		return nil
	},
}

func init() {
	// Persistent flags on meta parent
	metaCmd.PersistentFlags().StringVar(&metaToken, "token", "", "Meta API access token (default: WHATSAPP_ACCESS_TOKEN env)")
	metaCmd.PersistentFlags().StringVar(&metaAppSecret, "app-secret", "", "Meta App Secret for appsecret_proof (default: META_APP_SECRET env)")
	metaCmd.PersistentFlags().StringVar(&metaWABAID, "waba-id", "", "WhatsApp Business Account ID (default: "+defaultWABAID+")")
	metaCmd.PersistentFlags().StringVar(&metaAppID, "app-id", "", "Meta App ID (default: "+defaultAppID+")")
	metaCmd.PersistentFlags().StringVar(&metaOutput, "output", "", "Output format: text or json")

	// number-add flags
	metaNumberAddCmd.Flags().StringVar(&numberAddCC, "cc", "", "Country code (required)")
	metaNumberAddCmd.Flags().StringVar(&numberAddNumber, "number", "", "Phone number without country code (required)")
	_ = metaNumberAddCmd.MarkFlagRequired("cc")
	_ = metaNumberAddCmd.MarkFlagRequired("number")

	// register flags
	metaRegisterCmd.Flags().StringVar(&registerPhoneID, "phone-id", "", "Phone number ID from Meta (required)")
	metaRegisterCmd.Flags().StringVar(&registerPin, "pin", "", "6-digit PIN for two-step verification (required)")
	_ = metaRegisterCmd.MarkFlagRequired("phone-id")
	_ = metaRegisterCmd.MarkFlagRequired("pin")

	// deregister flags
	metaDeregisterCmd.Flags().StringVar(&deregisterPhoneID, "phone-id", "", "Phone number ID from Meta (required)")
	_ = metaDeregisterCmd.MarkFlagRequired("phone-id")

	// webhook-set flags
	metaWebhookSetCmd.Flags().StringVar(&webhookURL, "url", "", "Webhook callback URL (required)")
	metaWebhookSetCmd.Flags().StringVar(&webhookVerifyToken, "verify-token", "", "Webhook verify token (required)")
	metaWebhookSetCmd.Flags().StringVar(&webhookFields, "fields", "messages", "Comma-separated webhook fields")
	_ = metaWebhookSetCmd.MarkFlagRequired("url")
	_ = metaWebhookSetCmd.MarkFlagRequired("verify-token")

	// send flags
	metaSendCmd.Flags().StringVar(&sendTo, "to", "", "Recipient phone number with country code (required)")
	metaSendCmd.Flags().StringVar(&sendMessage, "message", "", "Message text to send (required)")
	_ = metaSendCmd.MarkFlagRequired("to")
	_ = metaSendCmd.MarkFlagRequired("message")

	// doctor flags
	metaDoctorCmd.Flags().BoolVar(&doctorSendTest, "send-test", false, "Send a test message as part of health check")
	metaDoctorCmd.Flags().StringVar(&doctorSendTo, "to", "", "Recipient for test message (requires --send-test)")

	// Wire up whatsapp subcommands
	metaWhatsAppCmd.AddCommand(metaNumbersCmd)
	metaWhatsAppCmd.AddCommand(metaNumberAddCmd)
	metaWhatsAppCmd.AddCommand(metaRegisterCmd)
	metaWhatsAppCmd.AddCommand(metaDeregisterCmd)
	metaWhatsAppCmd.AddCommand(metaWebhookGetCmd)
	metaWhatsAppCmd.AddCommand(metaWebhookSetCmd)
	metaWhatsAppCmd.AddCommand(metaSendCmd)

	// Wire up token subcommands
	metaTokenCmd.AddCommand(metaTokenCheckCmd)
	metaTokenCmd.AddCommand(metaTokenDebugCmd)

	// Wire up meta subcommands
	metaCmd.AddCommand(metaWhatsAppCmd)
	metaCmd.AddCommand(metaTokenCmd)
	metaCmd.AddCommand(metaDoctorCmd)
	metaCmd.AddCommand(metaMarketingCmd)
}
