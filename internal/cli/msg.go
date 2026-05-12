package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// `lw msg send` — Phase 3 / EB-001 plan §3.
//
// Thin HTTP client over the LightWave gateway's message-out endpoint.
// v_core uses this to surface persona output to Joel via Telegram /
// Slack / CLI without going through bash.
//
// The actual gateway lives in lightwave-sys (`zeroclaw-gateway` crate);
// this command never speaks directly to Telegram or Slack — it POSTs to
// the gateway, which fans out.
//
// New top-level domain not yet declared in lightwave-core schema. Wired
// hardcoded in root.go alongside agentCmd / memoryCmd; schema entry
// follows. Same dormancy pattern as #33 / #35.

var msgCmd = &cobra.Command{
	Use:   "msg",
	Short: "Gateway-mediated outbound messaging (Telegram / Slack / CLI)",
	Long: `Send a message through the LightWave gateway, which fans it out to the
configured channel adapter (Telegram thread, Slack channel, CLI tail).

Gateway URL: $LW_GATEWAY_URL (default http://localhost:9701).
v_core uses this to surface persona output to Joel without bash.`,
}

var (
	msgSendChannel  string
	msgSendPersona  string
	msgSendText     string
	msgSendTextFile string
	msgSendDryRun   bool
	msgSendJSON     bool
	msgSendTimeout  time.Duration
)

var msgSendCmd = &cobra.Command{
	Use:          "send",
	Short:        "POST a message to the gateway's /msg/send endpoint",
	SilenceUsage: true,
	Long: `POST a message to the gateway's /msg/send endpoint. The gateway is
responsible for actually delivering to Telegram / Slack / etc.

Required flags:
  --channel <name>   gateway-side channel id (e.g. "joel-telegram", "ops-slack")
  --persona <name>   actor identity stamped on the message (v_core, cpe, …)
  --text <string>    inline message body
  --text-file <path> read body from file (use - for stdin)

Examples:
  lw msg send --channel joel-tg --persona v_core --text "Sprint SPR-001 is full"
  lw msg send --channel ops-slack --persona cpe --text-file ./status.md
  echo "hello" | lw msg send --channel joel-tg --persona v_core --text-file -
  lw msg send --channel joel-tg --persona v_core --text "ping" --dry-run`,
	RunE: runMsgSend,
}

func init() {
	msgSendCmd.Flags().StringVar(&msgSendChannel, "channel", "", "Gateway channel id (required)")
	msgSendCmd.Flags().StringVar(&msgSendPersona, "persona", "", "Actor persona stamped on the message (required)")
	msgSendCmd.Flags().StringVar(&msgSendText, "text", "", "Inline message body")
	msgSendCmd.Flags().StringVar(&msgSendTextFile, "text-file", "", "Read body from file (- for stdin)")
	msgSendCmd.Flags().BoolVar(&msgSendDryRun, "dry-run", false, "Print request envelope; do not POST")
	msgSendCmd.Flags().BoolVar(&msgSendJSON, "json", false, "Emit JSON envelope on success")
	msgSendCmd.Flags().DurationVar(&msgSendTimeout, "timeout", 10*time.Second, "HTTP request timeout")
	_ = msgSendCmd.MarkFlagRequired("channel")
	_ = msgSendCmd.MarkFlagRequired("persona")

	msgCmd.AddCommand(msgSendCmd)
}

// gatewayBaseURL returns the configured gateway endpoint, mirroring the
// pattern used by augustaBaseURL() in council.go.
func gatewayBaseURL() string {
	if u := os.Getenv("LW_GATEWAY_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:9701"
}

// MsgSendRequest is the JSON shape POSTed to the gateway. Exported so
// the gateway implementation (in lightwave-sys) can vendor this as the
// contract.
type MsgSendRequest struct {
	Channel string `json:"channel"`
	Persona string `json:"persona"`
	Text    string `json:"text"`
}

// MsgSendResponse is the JSON shape the gateway returns on success.
type MsgSendResponse struct {
	MessageID  string `json:"message_id,omitempty"`
	DeliveryAt string `json:"delivery_at,omitempty"`
	Status     string `json:"status,omitempty"`
}

func runMsgSend(cmd *cobra.Command, _ []string) error {
	body, err := readMsgBody()
	if err != nil {
		return err
	}

	req := MsgSendRequest{
		Channel: msgSendChannel,
		Persona: msgSendPersona,
		Text:    body,
	}

	if msgSendDryRun {
		return previewMsgSend(req)
	}

	resp, err := postMsgSend(cmd.Context(), req)
	if err != nil {
		return err
	}

	if msgSendJSON {
		return emitJSON(resp)
	}
	fmt.Printf("delivered to %s via %s",
		color.CyanString(req.Channel), color.YellowString(req.Persona))
	if resp.MessageID != "" {
		fmt.Printf(" (id=%s)", resp.MessageID)
	}
	fmt.Println()
	return nil
}

func readMsgBody() (string, error) {
	switch {
	case msgSendTextFile == "" && msgSendText == "":
		return "", fmt.Errorf("supply --text or --text-file")
	case msgSendTextFile != "" && msgSendText != "":
		return "", fmt.Errorf("--text and --text-file are mutually exclusive")
	case msgSendTextFile == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	case msgSendTextFile != "":
		b, err := os.ReadFile(msgSendTextFile)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", msgSendTextFile, err)
		}
		return string(b), nil
	default:
		return msgSendText, nil
	}
}

func previewMsgSend(req MsgSendRequest) error {
	url := gatewayBaseURL() + "/msg/send"
	fmt.Println(color.CyanString("DRY RUN — no HTTP request issued"))
	fmt.Printf("POST %s\n", url)
	fmt.Printf("Channel: %s\n", req.Channel)
	fmt.Printf("Persona: %s\n", req.Persona)
	fmt.Printf("Body size: %d bytes\n", len(req.Text))
	preview := req.Text
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	fmt.Printf("Body preview:\n%s\n", preview)
	return nil
}

func postMsgSend(ctx context.Context, req MsgSendRequest) (*MsgSendResponse, error) {
	url := gatewayBaseURL() + "/msg/send"

	if ctx == nil {
		ctx = context.Background()
	}
	if msgSendTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, msgSendTimeout)
		defer cancel()
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "lw-msg")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		// Discriminate the "no gateway running" case with a friendlier
		// hint — common during MVP when the gateway crate isn't booted.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gateway POST timed out after %s (LW_GATEWAY_URL=%s)",
				msgSendTimeout, gatewayBaseURL())
		}
		return nil, fmt.Errorf("POST %s: %w (is the gateway running? set LW_GATEWAY_URL or boot zeroclaw-gateway)", url, err)
	}
	defer httpResp.Body.Close()

	respBytes, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read response: %w", readErr)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway returned HTTP %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var resp MsgSendResponse
	if len(respBytes) > 0 {
		// Some gateways return empty 200; that's fine.
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w (raw: %s)", err, string(respBytes))
		}
	}
	return &resp, nil
}
