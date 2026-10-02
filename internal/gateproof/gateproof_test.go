// Package gateproof_test is a DO NOT MERGE plant: a test that always fails, so
// the repo's required check is shown able to refuse a PR
// (lightwave-core ops/factory-lane-eligibility, ledger_event gate.proof).
package gateproof_test

import "testing"

func TestGateProofPlantedFailure(t *testing.T) {
	t.Parallel()
	t.Fatal("planted failure: the required check must refuse this PR")
}
