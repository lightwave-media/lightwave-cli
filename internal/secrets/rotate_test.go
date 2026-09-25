package secrets_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

const (
	rotatorRole = "lightwave-secret-rotator"
	tokenName   = "NULLTICKETS_API_TOKEN"
)

type fakeMeta struct {
	err  error
	meta secrets.ParamMeta
}

func (f *fakeMeta) Meta(context.Context, string) (secrets.ParamMeta, error) { return f.meta, f.err }

type fakeWriter struct {
	err     error
	caller  secrets.Caller
	written [][]byte
	version int64
}

func (f *fakeWriter) Caller() secrets.Caller { return f.caller }

func (f *fakeWriter) Put(_ context.Context, _ string, value []byte) (int64, error) {
	f.written = append(f.written, bytes.Clone(value))

	return f.version, f.err
}

// rig records every effect Rotate performs.
type rig struct {
	deps    *secrets.RotateDeps
	writer  *fakeWriter
	meta    *fakeMeta
	statusF func(keys []string) string
	kicked  []string
	probed  [][]string
	ledger  []secrets.LedgerRow
	sleeps  int
}

func newRig(session string) *rig {
	r := &rig{
		writer:  &fakeWriter{caller: secrets.Caller{Role: rotatorRole, Session: session}, version: 8},
		meta:    &fakeMeta{meta: secrets.ParamMeta{Type: "SecureString", KeyID: "alias/aws/ssm", Tier: "Standard", Version: 7}},
		statusF: func([]string) string { return "200" },
	}
	r.deps = &secrets.RotateDeps{
		Meta:   r.meta,
		Writer: r.writer,
		Random: rand.Reader,
		Kickstart: func(_ context.Context, label string) error {
			r.kicked = append(r.kicked, label)

			return nil
		},
		Probe: func(_ context.Context, keys []string, _ string) (string, error) {
			r.probed = append(r.probed, keys)

			return r.statusF(keys), nil
		},
		Ledger: func(row secrets.LedgerRow) error {
			r.ledger = append(r.ledger, row)

			return nil
		},
		Now:   func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
		Sleep: func(time.Duration) { r.sleeps++ },
	}

	return r
}

func (r *rig) events() []string {
	out := make([]string, 0, len(r.ledger))
	for _, row := range r.ledger {
		out = append(out, row.Event)
	}

	return out
}

// assertNoValue fails if the written value appears in the line or the ledger.
func (r *rig) assertNoValue(t *testing.T, line string) {
	t.Helper()

	require.Len(t, r.writer.written, 1)
	value := string(r.writer.written[0])
	assert.NotContains(t, line, value)

	for _, row := range r.ledger {
		assert.NotContains(t, row.Detail, value)
		assert.Equal(t, []string{tokenName}, row.Params)
	}
}

func tokenRecord() secrets.Record {
	return secrets.Record{
		Name: tokenName, SSMPath: secrets.Path + tokenName, Status: "active", RotationMode: "generate",
		WriterIdentity: rotatorRole, Store: "ssm", Generate: &secrets.GenerateSpec{Charset: "hex", Length: 48},
	}
}

func loads(keys ...string) []secrets.SecretLoading {
	out := make([]secrets.SecretLoading, 0, len(keys))
	for _, k := range keys {
		soft := strings.HasSuffix(k, "?")
		out = append(out, secrets.SecretLoading{SSMPath: secrets.Path + strings.TrimSuffix(k, "?"), SoftFail: soft})
	}

	return out
}

func daemon(id, action, target, probe string, keys ...string) secrets.Daemon {
	d := secrets.Daemon{ID: id, Refresh: &secrets.RefreshSpec{Action: action, Target: target}, SecretLoadings: loads(keys...)}
	if probe != "" {
		d.Verify = &secrets.VerifySpec{Probe: probe}
	}

	return d
}

// liveShape mirrors the consumers NULLTICKETS_API_TOKEN had on 2026-09-24.
func liveShape(extra ...secrets.Daemon) *secrets.Map {
	return &secrets.Map{
		Records: []secrets.Record{tokenRecord()},
		Daemons: append([]secrets.Daemon{
			daemon("nulltickets-tracker", "kickstart", "com.nullhub.server", "probe-tracker", tokenName),
			daemon("nullboiler-boiler", "kickstart", "com.nullhub.server", "", tokenName, "OPENROUTER_API_KEY?"),
			daemon("lw-webhook", "kickstart", "com.lightwave.webhook", "probe-webhook", tokenName, "LW_WEBHOOK_SECRET?"),
			daemon("knowledge-promote-cron", "none", "", "", "NOTION_API_KEY", tokenName),
			daemon("unrelated", "kickstart", "com.example.other", "probe-other", "OTHER_KEY"),
		}, extra...),
	}
}

func TestRotateWritesOnceRefreshesEachTargetOnceAndProbes(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	res, err := secrets.Rotate(context.Background(), r.deps, liveShape(), tokenName)
	require.NoError(t, err)

	assert.Equal(t, secrets.ExitDone, res.ExitCode())
	assert.Equal(t, []string{"com.nullhub.server", "com.lightwave.webhook"}, r.kicked)
	assert.Equal(t, [][]string{{tokenName}, {tokenName}}, r.probed, "soft-fail keys stay out of --only")
	assert.Equal(t, []string{"rotated", "rotation-verified"}, r.events())
	assert.Equal(t,
		tokenName+": rotated v7 -> v8 as op_joel; refreshed: com.nullhub.server, com.lightwave.webhook; probes ok: nulltickets-tracker, lw-webhook",
		res.Line())

	value := string(r.writer.written[0])
	assert.Len(t, value, 48)
	assert.Empty(t, strings.Trim(value, "0123456789abcdef"), "hex charset only")
	r.assertNoValue(t, res.Line())
}

func TestRotateReportsAConsumerItCannotRefreshAsPending(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	site := daemon("site-release", "redeploy", "lightwave-media/site:release.yml", "probe-site", tokenName)

	res, err := secrets.Rotate(context.Background(), r.deps, liveShape(site), tokenName)
	require.NoError(t, err)

	assert.Equal(t, secrets.ExitPending, res.ExitCode())
	assert.Equal(t, []string{"site-release needs redeploy lightwave-media/site:release.yml"}, res.Pending)
	assert.Len(t, r.probed, 2, "a consumer still on the old value is not probed")
	assert.Equal(t, []string{"rotated", "rotation-incomplete"}, r.events())
	r.assertNoValue(t, res.Line())
}

func TestRotateRetriesAProbeThenReportsItsStatus(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	r.statusF = func([]string) string {
		if len(r.probed) < 3 {
			return "401"
		}

		return "200"
	}

	res, err := secrets.Rotate(context.Background(), r.deps, liveShape(), tokenName)
	require.NoError(t, err)
	assert.Equal(t, secrets.ExitDone, res.ExitCode(), "a consumer coming back up passes on a later try")
	assert.Equal(t, 2, r.sleeps)

	r = newRig("op_joel")
	r.statusF = func([]string) string { return "401" }

	res, err = secrets.Rotate(context.Background(), r.deps, liveShape(), tokenName)
	require.NoError(t, err)
	assert.Equal(t, secrets.ExitFailed, res.ExitCode())
	assert.Contains(t, res.Failures, "probe nulltickets-tracker: status 401 after 10 tries")
	assert.Equal(t, []string{"rotated", "rotation-incomplete"}, r.events(), "the write is on record even though a probe failed")
	r.assertNoValue(t, res.Line())
}

func TestRotateRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		edit    func(*secrets.Record, *rig)
		name    string
		session string
	}{
		"unknown key":            {name: "NOPE"},
		"alias":                  {name: "OLD_TOKEN", edit: func(rec *secrets.Record, _ *rig) { rec.Aliases = []string{secrets.Path + "OLD_TOKEN"} }},
		"pending":                {edit: func(rec *secrets.Record, _ *rig) { rec.Status = "pending" }},
		"retire":                 {edit: func(rec *secrets.Record, _ *rig) { rec.Status = "retire" }},
		"human inbox":            {edit: func(rec *secrets.Record, _ *rig) { rec.RotationMode = "human_inbox" }},
		"vendor api":             {edit: func(rec *secrets.Record, _ *rig) { rec.RotationMode = "vendor_api" }},
		"local file":             {edit: func(rec *secrets.Record, _ *rig) { rec.Store = "local_file" }},
		"pem keypair":            {edit: func(rec *secrets.Record, _ *rig) { rec.Generate.Charset = "pem_keypair" }},
		"too short":              {edit: func(rec *secrets.Record, _ *rig) { rec.Generate.Length = 16 }},
		"has aliases":            {edit: func(rec *secrets.Record, _ *rig) { rec.Aliases = []string{secrets.Path + "OLD"} }},
		"has mirrors":            {edit: func(rec *secrets.Record, _ *rig) { rec.Mirrors = []string{"gh:repo"} }},
		"literal peer":           {edit: func(rec *secrets.Record, _ *rig) { rec.PeersLiteral = []string{"config.json:api_token"} }},
		"persona not a rotator":  {session: "v_cli-developer"},
		"wrong role":             {edit: func(_ *secrets.Record, r *rig) { r.writer.caller.Role = "lightwave-admin-role" }},
		"customer managed key":   {edit: func(_ *secrets.Record, r *rig) { r.meta.meta.KeyID = "alias/custom" }},
		"advanced tier":          {edit: func(_ *secrets.Record, r *rig) { r.meta.meta.Tier = "Advanced" }},
		"plain string parameter": {edit: func(_ *secrets.Record, r *rig) { r.meta.meta.Type = "String" }},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			session := tc.session
			if session == "" {
				session = "op_joel"
			}

			r := newRig(session)
			m := liveShape()
			if tc.edit != nil {
				tc.edit(&m.Records[0], r)
			}

			name := tc.name
			if name == "" {
				name = tokenName
			}

			res, err := secrets.Rotate(context.Background(), r.deps, m, name)
			require.ErrorIs(t, err, secrets.ErrRefused)
			assert.Nil(t, res)
			assert.Equal(t, secrets.ExitRefused, secrets.ErrorExitCode(err))
			assert.Empty(t, r.writer.written, "nothing written")
			assert.Empty(t, r.ledger, "no ledger row")
			assert.Empty(t, r.kicked, "no consumer touched")
		})
	}
}

func TestRotateLetsAListedPersonaRotate(t *testing.T) {
	t.Parallel()

	r := newRig("v_cli-developer")
	m := liveShape()
	m.Records[0].Rotators = []string{"v_cli-developer"}

	res, err := secrets.Rotate(context.Background(), r.deps, m, tokenName)
	require.NoError(t, err)
	assert.Equal(t, "v_cli-developer", r.ledger[0].Session)
	assert.Equal(t, secrets.ExitDone, res.ExitCode())
}

func TestRotateDryRunChecksEverythingAndWritesNothing(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	r.deps.DryRun = true

	res, err := secrets.Rotate(context.Background(), r.deps, liveShape(), tokenName)
	require.NoError(t, err)
	assert.Equal(t, tokenName+": dry run as op_joel: checks passed at v7; nothing written", res.Line())
	assert.Empty(t, r.writer.written)
	assert.Empty(t, r.ledger)
	assert.Empty(t, r.kicked)
}

func TestRotateFailedWriteIsNotRecordedAsARotation(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	r.writer.err = errors.New("AccessDeniedException")

	res, err := secrets.Rotate(context.Background(), r.deps, liveShape(), tokenName)
	require.ErrorContains(t, err, tokenName+": write: AccessDeniedException")
	assert.Nil(t, res)
	assert.Equal(t, secrets.ExitFailed, secrets.ErrorExitCode(err))
	assert.Empty(t, r.ledger)
	assert.Empty(t, r.kicked)
}

func TestGenerateDrawsEvenlyFromTheAlphabetOnly(t *testing.T) {
	t.Parallel()

	for charset, alphabet := range map[string]string{
		"alnum":     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
		"hex":       "0123456789abcdef",
		"base64url": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_",
	} {
		value, err := secrets.Generate(rand.Reader, &secrets.GenerateSpec{Charset: charset, Length: 200})
		require.NoError(t, err)
		assert.Len(t, value, 200)
		assert.Empty(t, strings.Trim(string(value), alphabet), charset)
	}

	// 0xFF is past the last whole multiple of 62, so alnum must reject it.
	value, err := secrets.Generate(bytes.NewReader(bytes.Repeat([]byte{0xFF, 0x00}, 64)), &secrets.GenerateSpec{Charset: "alnum", Length: 32})
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("A", 32), string(value))

	_, err = secrets.Generate(bytes.NewReader(nil), &secrets.GenerateSpec{Charset: "hex", Length: 32})
	require.Error(t, err)

	_, err = secrets.Generate(rand.Reader, &secrets.GenerateSpec{Charset: "pem_keypair", Length: 64})
	require.Error(t, err)
}
