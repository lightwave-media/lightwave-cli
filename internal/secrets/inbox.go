package secrets

// inbox.go — `lw secret inbox` for human_inbox keys (lightwave-cli#547,
// slice 2): a value a vendor issues only from its console. The operator
// pastes it once on stdin; it is written as the rotator role through the same
// writer as rotate, then the map's consumers are refreshed and probed. The
// value is never printed, logged or recorded, as for rotate: output and the
// ledger carry names and versions.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// The SSM Standard tier holds at most 4 KB.
const maxInboxValue = 4096

const inboxSurface = "lw secret inbox"

// CheckInboxRecord refuses a key lw secret inbox must not write.
func CheckInboxRecord(rec *Record) error {
	switch {
	case rec.Status != "pending" && rec.Status != "active" && rec.Status != "dormant":
		return refuse(rec.Name, "status "+rec.Status+"; only pending, active or dormant keys are written")
	case rec.RotationMode != "human_inbox":
		return refuse(rec.Name, "rotation_mode "+rec.RotationMode+" is not human_inbox; generate keys use lw secret rotate")
	default:
		return checkSingleCopy(rec)
	}
}

// Inbox writes value as the key called name, creating the parameter (tagged
// app and managed-by) when SSM does not have it yet, then refreshes and probes
// its consumers. In a dry run value is ignored and may be nil. The caller
// clears value.
func Inbox(ctx context.Context, deps *RotateDeps, m *Map, name string, value []byte) (*Result, error) {
	rec, err := m.Lookup(name)
	if err != nil {
		return nil, err
	}

	if err := CheckInboxRecord(rec); err != nil {
		return nil, err
	}

	caller := deps.Writer.Caller()
	if err := CheckCaller(rec, caller); err != nil {
		return nil, err
	}

	if !deps.DryRun {
		if err := checkInboxValue(rec, value); err != nil {
			return nil, err
		}
	}

	meta, err := deps.Meta.Meta(ctx, rec.SSMPath)
	exists := err == nil

	switch {
	case errors.Is(err, ErrNotFound):
		meta = ParamMeta{} // a new parameter starts at version 0, whatever came back with the error
	case err != nil:
		return nil, fmt.Errorf("%s: read metadata: %w", rec.Name, err)
	case meta.Type != wantType || meta.KeyID != wantKeyID || meta.Tier != wantTier:
		return nil, refuse(rec.Name, fmt.Sprintf("the parameter is %s/%s/%s; inbox writes only %s/%s/%s",
			meta.Type, meta.KeyID, meta.Tier, wantType, wantKeyID, wantTier))
	}

	res := &Result{Name: rec.Name, Session: caller.Session, From: meta.Version, DryRun: deps.DryRun, Surface: inboxSurface}
	if deps.DryRun {
		return res, nil
	}

	if exists {
		res.To, err = deps.Writer.Put(ctx, rec.SSMPath, value)
	} else {
		res.To, err = deps.Writer.Create(ctx, rec.SSMPath, value, inboxTags(rec))
	}

	if err != nil {
		return nil, fmt.Errorf("%s: write: %w", rec.Name, err)
	}

	res.record(deps, "inbox-written")
	res.followUp(ctx, deps, rec, m.Consumers(rec))

	res.MapPending = rec.Status == "pending"

	if res.ExitCode() == ExitDone {
		res.record(deps, "inbox-verified")
	} else {
		res.record(deps, "inbox-incomplete")
	}

	return res, nil
}

// checkInboxValue refuses an empty, oversized or malformed paste before any
// AWS call. The vendor's value_prefix, when the map records one, catches the
// wrong token pasted for the right name. No message quotes the value.
func checkInboxValue(rec *Record, value []byte) error {
	switch {
	case len(value) == 0:
		return refuse(rec.Name, "no value on stdin")
	case len(value) > maxInboxValue:
		return refuse(rec.Name, fmt.Sprintf("the value is over %d bytes", maxInboxValue))
	case bytes.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r == 0x7f }):
		return refuse(rec.Name, "the value contains whitespace or a control character; paste the token alone")
	}

	if prefix := vendorPrefix(rec); prefix != "" && !bytes.HasPrefix(value, []byte(prefix)) {
		return refuse(rec.Name, "the value does not start with "+prefix+", the prefix "+rec.Name+"'s vendor issues")
	}

	return nil
}

// vendorPrefix reads vendor.value_prefix, which the map decodes loosely.
func vendorPrefix(rec *Record) string {
	vendor, _ := rec.Vendor.(map[string]any)
	prefix, _ := vendor["value_prefix"].(string)

	return prefix
}

// inboxTags are the tags CLAUDE.md §5 requires on a new runtime secret.
func inboxTags(rec *Record) map[string]string {
	app := rec.OwnerPersona
	if app == "" {
		app = rec.Name
	}

	return map[string]string{"app": app, "managed-by": inboxSurface}
}
