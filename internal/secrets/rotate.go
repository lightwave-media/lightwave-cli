package secrets

// rotate.go — `lw secret rotate` for generate-mode keys (lightwave-cli#547,
// slice 1). The successor value is made in this process, written once to SSM
// as the rotator role, and cleared. Nothing prints, logs or records it:
// consumers re-read it from SSM when kickstarted, and each probe re-reads it
// through `lw config exec`, so no copy is handed on.
//
// What enforces what. IAM is the boundary: the rotator role may write
// /lightwave/prod values and never read them; lightwave-admin may assume it
// only as op_*, lightwave-agent only as v_*. The checks here are guard rails
// against mistakes. A v_* session name is chosen by its caller, so `rotators`
// separates personas that follow the map, not personas that lie about who
// they are.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Exit codes of lw secret rotate.
const (
	ExitDone    = 0
	ExitFailed  = 1
	ExitPending = 3
	ExitRefused = 4
)

// Every /lightwave/prod SecureString has this shape (measured 2026-09-24).
// A parameter with any other shape is refused rather than silently rewritten.
const (
	wantType  = "SecureString"
	wantKeyID = "alias/aws/ssm"
	wantTier  = "Standard"

	minLength     = 32
	byteValues    = 256
	randomChunk   = 64
	ledgerSurface = "lw secret rotate"
	probeAttempts = 10
	probeDelay    = 2 * time.Second
)

var charsets = map[string]string{
	"alnum":     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
	"hex":       "0123456789abcdef",
	"base64url": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_",
}

// passing is the only probe output that counts as success: an HTTP 2xx code
// or "ok". Probes built on `curl -s` exit 0 on a 401, so exit status alone
// proves nothing.
var passing = regexp.MustCompile(`^(2[0-9][0-9]|ok)$`)

// ParamMeta is what DescribeParameters reports about a parameter. No value.
type ParamMeta struct {
	Type    string
	KeyID   string
	Tier    string
	Version int64
}

// Caller is the identity a write runs as, as IAM reports it.
type Caller struct {
	Role    string
	Session string
}

// Operator reports whether the session is a human operator's (op_*).
func (c Caller) Operator() bool { return strings.HasPrefix(c.Session, "op_") }

// MetaReader reads parameter metadata.
type MetaReader interface {
	Meta(ctx context.Context, path string) (ParamMeta, error)
}

// ValueWriter writes a parameter value as the identity Caller reports.
type ValueWriter interface {
	Caller() Caller
	Put(ctx context.Context, path string, value []byte) (int64, error)
}

// LedgerRow is one NAME-only row of secrets-exposure.jsonl.
type LedgerRow struct {
	TS      string   `json:"ts"`
	Session string   `json:"session"`
	Event   string   `json:"event"`
	Surface string   `json:"surface"`
	Detail  string   `json:"detail"`
	Params  []string `json:"params"`
}

// RotateDeps are the effects Rotate performs, each replaceable in tests.
type RotateDeps struct {
	Meta      MetaReader
	Writer    ValueWriter
	Random    io.Reader
	Kickstart func(ctx context.Context, label string) error
	Probe     func(ctx context.Context, keys []string, probe string) (string, error)
	Ledger    func(LedgerRow) error
	Now       func() time.Time
	Sleep     func(time.Duration)
	DryRun    bool
}

// Result is what a rotation did, by name and version only.
type Result struct {
	Name      string
	Session   string
	Refreshed []string
	Probed    []string
	Pending   []string
	Failures  []string
	From      int64
	To        int64
	DryRun    bool
}

// ErrorExitCode maps a Rotate error to its exit code.
func ErrorExitCode(err error) int {
	if errors.Is(err, ErrRefused) {
		return ExitRefused
	}

	return ExitFailed
}

// ExitCode is 1 when a follow-up failed, 3 when one is owed by someone else.
func (r *Result) ExitCode() int {
	switch {
	case len(r.Failures) > 0:
		return ExitFailed
	case len(r.Pending) > 0:
		return ExitPending
	default:
		return ExitDone
	}
}

// Line is the one line printed and recorded.
func (r *Result) Line() string {
	if r.DryRun {
		return fmt.Sprintf("%s: dry run as %s: checks passed at v%d; nothing written", r.Name, r.Session, r.From)
	}

	parts := []string{fmt.Sprintf("%s: rotated v%d -> v%d as %s", r.Name, r.From, r.To, r.Session)}
	for _, part := range []struct {
		label string
		items []string
	}{
		{"refreshed", r.Refreshed},
		{"probes ok", r.Probed},
		{"pending", r.Pending},
		{"failed", r.Failures},
	} {
		if len(part.items) > 0 {
			parts = append(parts, part.label+": "+strings.Join(part.items, ", "))
		}
	}

	return strings.Join(parts, "; ")
}

// CheckRecord refuses a key lw secret rotate must not rotate.
func CheckRecord(rec *Record) error {
	switch {
	case rec.Status != "active" && rec.Status != "dormant":
		return refuse(rec.Name, "status "+rec.Status+"; only active or dormant keys rotate")
	case rec.RotationMode != "generate":
		return refuse(rec.Name, "rotation_mode "+rec.RotationMode+" is not handled by lw secret rotate yet; use the rotate-aws-secret runbook")
	case rec.Store != "ssm":
		return refuse(rec.Name, "store "+rec.Store+"; only SSM keys rotate")
	case rec.Generate == nil || charsets[rec.Generate.Charset] == "":
		return refuse(rec.Name, "generate.charset must be alnum, hex or base64url")
	case rec.Generate.Length < minLength:
		return refuse(rec.Name, fmt.Sprintf("generate.length is under %d", minLength))
	case len(rec.Aliases) > 0:
		return refuse(rec.Name, "its aliases would keep serving the old value")
	case len(rec.Mirrors) > 0:
		return refuse(rec.Name, "it has mirrors, which lw secret rotate does not update yet")
	case len(rec.PeersLiteral) > 0:
		return refuse(rec.Name, "a peer holds a literal copy (peers_literal) that would go stale; move it to SSM first")
	default:
		return nil
	}
}

// CheckCaller refuses a write that would not run as the record's writer
// identity, or by a persona the record does not list.
func CheckCaller(rec *Record, c Caller) error {
	if c.Role != rec.WriterIdentity {
		return refuse(rec.Name, "writes run only as "+rec.WriterIdentity+", not "+c.Role)
	}

	if c.Operator() || slices.Contains(rec.Rotators, c.Session) {
		return nil
	}

	return refuse(rec.Name, c.Session+" is not in its rotators (empty means the operator only)")
}

// Rotate replaces the value of the key called name with a generated one,
// refreshes its consumers and probes them. A refusal or a failed write
// returns an error; after a write, follow-up failures land in the Result.
func Rotate(ctx context.Context, deps *RotateDeps, m *Map, name string) (*Result, error) {
	rec, err := m.Lookup(name)
	if err != nil {
		return nil, err
	}

	if err := CheckRecord(rec); err != nil {
		return nil, err
	}

	caller := deps.Writer.Caller()
	if err := CheckCaller(rec, caller); err != nil {
		return nil, err
	}

	meta, err := deps.Meta.Meta(ctx, rec.SSMPath)
	if err != nil {
		return nil, fmt.Errorf("%s: read metadata: %w", rec.Name, err)
	}

	if meta.Type != wantType || meta.KeyID != wantKeyID || meta.Tier != wantTier {
		return nil, refuse(rec.Name, fmt.Sprintf("the parameter is %s/%s/%s; rotate writes only %s/%s/%s",
			meta.Type, meta.KeyID, meta.Tier, wantType, wantKeyID, wantTier))
	}

	res := &Result{Name: rec.Name, Session: caller.Session, From: meta.Version, DryRun: deps.DryRun}
	if deps.DryRun {
		return res, nil
	}

	res.To, err = writeSuccessor(ctx, deps, rec)
	if err != nil {
		return nil, fmt.Errorf("%s: write: %w", rec.Name, err)
	}

	// The value has changed. Record that before anything else can fail.
	res.record(deps, "rotated")
	res.followUp(ctx, deps, rec, m.Consumers(rec))

	if res.ExitCode() == ExitDone {
		res.record(deps, "rotation-verified")
	} else {
		res.record(deps, "rotation-incomplete")
	}

	return res, nil
}

func writeSuccessor(ctx context.Context, deps *RotateDeps, rec *Record) (int64, error) {
	value, err := Generate(deps.Random, rec.Generate)
	if err != nil {
		return 0, err
	}
	defer clear(value)

	return deps.Writer.Put(ctx, rec.SSMPath, value)
}

func (r *Result) record(deps *RotateDeps, event string) {
	row := LedgerRow{
		TS:      deps.Now().UTC().Format(time.RFC3339),
		Session: r.Session,
		Event:   event,
		Surface: ledgerSurface,
		Detail:  r.Line(),
		Params:  []string{r.Name},
	}
	if err := deps.Ledger(row); err != nil {
		r.Failures = append(r.Failures, "ledger "+event+": "+err.Error())
	}
}

// followUp kickstarts each launchd target once, reports refreshes it cannot
// run as pending, then probes every consumer that is not pending.
func (r *Result) followUp(ctx context.Context, deps *RotateDeps, rec *Record, consumers []*Daemon) {
	kicked := map[string]bool{}
	waiting := map[*Daemon]bool{}

	for _, d := range consumers {
		action, target := "none", ""
		if d.Refresh != nil {
			action, target = d.Refresh.Action, d.Refresh.Target
		}

		switch {
		case action == "none":
		case action == "kickstart" && kicked[target]:
		case action == "kickstart":
			kicked[target] = true
			if err := deps.Kickstart(ctx, target); err != nil {
				r.Failures = append(r.Failures, "kickstart "+target+": "+err.Error())
			} else {
				r.Refreshed = append(r.Refreshed, target)
			}
		default:
			waiting[d] = true
			r.Pending = append(r.Pending, d.ID+" needs "+action+" "+target)
		}
	}

	for _, d := range consumers {
		if waiting[d] || d.Verify == nil || d.Verify.Probe == "" {
			continue
		}

		if status := probeUntilPass(ctx, deps, probeKeys(d, rec), d.Verify.Probe); status != "" {
			r.Failures = append(r.Failures, "probe "+d.ID+": "+status)
		} else {
			r.Probed = append(r.Probed, d.ID)
		}
	}
}

// probeUntilPass retries while a kickstarted consumer comes back up. It
// returns "" on a pass, else why the last attempt failed.
func probeUntilPass(ctx context.Context, deps *RotateDeps, keys []string, probe string) string {
	why := ""

	for attempt := range probeAttempts {
		if attempt > 0 {
			deps.Sleep(probeDelay)
		}

		status, err := deps.Probe(ctx, keys, probe)
		if err == nil && passing.MatchString(status) {
			return ""
		}

		why = "no 2xx or ok status"
		if status != "" {
			why = "status " + status
		}
	}

	return why + fmt.Sprintf(" after %d tries", probeAttempts)
}

// probeKeys are the rotated key plus every key the consumer cannot start
// without: a probe runs under `lw config exec --only`, which fails closed on a
// missing key, so an optional one the store lacks would sink it.
func probeKeys(d *Daemon, rec *Record) []string {
	keys := []string{rec.Name}

	for _, l := range d.SecretLoadings {
		name := strings.TrimPrefix(l.SSMPath, Path)
		if !l.SoftFail && !slices.Contains(keys, name) {
			keys = append(keys, name)
		}
	}

	return keys
}

// Generate returns spec.Length characters from spec's alphabet, drawn from
// random by rejection sampling so every character is equally likely. The
// caller clears the result.
func Generate(random io.Reader, spec *GenerateSpec) ([]byte, error) {
	alphabet := charsets[spec.Charset]
	if alphabet == "" || spec.Length < minLength {
		return nil, errors.New("generate: unsupported charset or length")
	}

	limit := byteValues - byteValues%len(alphabet)
	out := make([]byte, 0, spec.Length)
	buf := make([]byte, randomChunk)

	defer clear(buf)

	for len(out) < spec.Length {
		if _, err := io.ReadFull(random, buf); err != nil {
			clear(out)

			return nil, fmt.Errorf("generate: read random: %w", err)
		}

		for _, b := range buf {
			if int(b) < limit && len(out) < spec.Length {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}

	return out, nil
}
