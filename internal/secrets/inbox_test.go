package secrets_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

const (
	notionKey   = "NOTION_API_KEY"
	notionValue = "ntn_fixtureValueThatMustNeverAppear0123456789"
)

// inboxShape is a human_inbox key loaded by two consumers, one probed.
func inboxShape(status string) *secrets.Map {
	return &secrets.Map{
		Records: []secrets.Record{{
			Name: notionKey, SSMPath: secrets.Path + notionKey, Status: status, RotationMode: "human_inbox",
			WriterIdentity: rotatorRole, OwnerPersona: "v_security-compliance", Store: "ssm",
			Vendor: map[string]any{"name": "notion", "value_prefix": "ntn_"},
		}},
		Daemons: []secrets.Daemon{
			daemon("lw-webhook", "kickstart", "com.lightwave.webhook", "probe-webhook", notionKey, tokenName),
			daemon("knowledge-promote-cron", "none", "", "", notionKey),
		},
	}
}

func inboxLedgerHasNoValue(t *testing.T, r *rig, line string) {
	t.Helper()

	assert.NotContains(t, line, notionValue)

	for _, row := range r.ledger {
		assert.NotContains(t, fmt.Sprint(row), notionValue)
		assert.Equal(t, []string{notionKey}, row.Params)
		assert.Equal(t, "lw secret inbox", row.Surface)
	}
}

func TestInboxOverwritesAnActiveKeyRefreshesAndProbes(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	res, err := secrets.Inbox(context.Background(), r.deps, inboxShape("active"), notionKey, []byte(notionValue))
	require.NoError(t, err)

	assert.Equal(t, secrets.ExitDone, res.ExitCode())
	assert.Equal(t, 0, r.writer.created, "an existing parameter is overwritten, not created")
	assert.Equal(t, [][]byte{[]byte(notionValue)}, r.writer.written)
	assert.Equal(t, []string{"com.lightwave.webhook"}, r.kicked)
	assert.Equal(t, [][]string{{notionKey, tokenName}}, r.probed)
	assert.Equal(t, []string{"inbox-written", "inbox-verified"}, r.events())
	assert.Equal(t,
		notionKey+": written v7 -> v8 as op_joel; refreshed: com.lightwave.webhook; probes ok: lw-webhook",
		res.Line())
	inboxLedgerHasNoValue(t, r, res.Line())
}

func TestInboxCreatesAMissingKeyTaggedAndReportsThePendingRow(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	r.meta.err = fmt.Errorf("%s: %w", secrets.Path+notionKey, secrets.ErrNotFound)
	r.writer.version = 1

	res, err := secrets.Inbox(context.Background(), r.deps, inboxShape("pending"), notionKey, []byte(notionValue))
	require.NoError(t, err)

	assert.Equal(t, 1, r.writer.created)
	assert.Equal(t, map[string]string{"app": "v_security-compliance", "managed-by": "lw secret inbox"}, r.writer.tags)
	assert.Equal(t, int64(0), res.From)
	assert.Equal(t, int64(1), res.To)
	assert.Equal(t, secrets.ExitMapPending, res.ExitCode(), "only the map row is owed: 5, not 3")
	assert.True(t, res.MapPending)
	assert.Empty(t, res.Pending, "no consumer refresh is owed")
	assert.Contains(t, res.Line(), "secret map: still pending; set "+notionKey+" active in gen_security_instances.py")
	assert.Equal(t, []string{"inbox-written", "inbox-incomplete"}, r.events())
	inboxLedgerHasNoValue(t, r, res.Line())
}

func TestInboxRefreshOwedOutranksAPendingMapRow(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	m := inboxShape("pending")
	m.Daemons = append(m.Daemons, daemon("site-release", "redeploy", "lightwave-media/site:release.yml", "", notionKey))

	res, err := secrets.Inbox(context.Background(), r.deps, m, notionKey, []byte(notionValue))
	require.NoError(t, err)
	assert.True(t, res.MapPending)
	assert.Equal(t, secrets.ExitPending, res.ExitCode(), "precedence 1 > 3 > 5")
}

func TestInboxRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		edit  func(*secrets.Record, *rig)
		value string
	}{
		"generate key":        {edit: func(rec *secrets.Record, _ *rig) { rec.RotationMode = "generate" }},
		"vendor api key":      {edit: func(rec *secrets.Record, _ *rig) { rec.RotationMode = "vendor_api" }},
		"retired":             {edit: func(rec *secrets.Record, _ *rig) { rec.Status = "retire" }},
		"local file":          {edit: func(rec *secrets.Record, _ *rig) { rec.Store = "local_file" }},
		"literal peer":        {edit: func(rec *secrets.Record, _ *rig) { rec.PeersLiteral = []string{"~/.claude.json"} }},
		"persona not listed":  {edit: func(_ *secrets.Record, r *rig) { r.writer.caller.Session = "v_cli-developer" }},
		"wrong role":          {edit: func(_ *secrets.Record, r *rig) { r.writer.caller.Role = "lightwave-admin-role" }},
		"plain string":        {edit: func(_ *secrets.Record, r *rig) { r.meta.meta.Type = "String" }},
		"empty":               {value: "-"},
		"wrong vendor prefix": {value: "secret_notTheNotionToken0123456789"},
		"two tokens":          {value: "ntn_one ntn_two"},
		"trailing newline":    {value: notionValue + "\n"},
		"too long":            {value: "ntn_" + string(make([]byte, 5000))},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			r := newRig("op_joel")
			m := inboxShape("active")
			if tc.edit != nil {
				tc.edit(&m.Records[0], r)
			}

			value := []byte(notionValue)
			switch tc.value {
			case "":
			case "-":
				value = nil
			default:
				value = []byte(tc.value)
			}

			res, err := secrets.Inbox(context.Background(), r.deps, m, notionKey, value)
			require.ErrorIs(t, err, secrets.ErrRefused)
			assert.Nil(t, res)
			assert.NotContains(t, err.Error(), "ntn_one", "a refusal never quotes the value")
			assert.Empty(t, r.writer.written, "nothing written")
			assert.Empty(t, r.ledger, "no ledger row")
			assert.Empty(t, r.kicked, "no consumer touched")
		})
	}
}

func TestInboxDryRunIgnoresTheValueAndWritesNothing(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	r.deps.DryRun = true

	res, err := secrets.Inbox(context.Background(), r.deps, inboxShape("active"), notionKey, nil)
	require.NoError(t, err)
	assert.Equal(t, notionKey+": dry run as op_joel: checks passed at v7; nothing written", res.Line())
	assert.Empty(t, r.writer.written)
	assert.Empty(t, r.ledger)
}

func TestRotateNowPointsAnInboxKeyAtInbox(t *testing.T) {
	t.Parallel()

	r := newRig("op_joel")
	_, err := secrets.Rotate(context.Background(), r.deps, inboxShape("active"), notionKey)
	require.ErrorIs(t, err, secrets.ErrRefused)
	assert.ErrorContains(t, err, "lw secret inbox")
}
