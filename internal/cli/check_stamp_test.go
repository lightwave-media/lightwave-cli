//nolint:testpackage // exercises the unexported pin table and contract reader
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckStamp_PinnedContractsMatchTheEmbeddedStamp is the live guard, not a
// fixture. It compares the versions this binary claims to implement against the
// versions the embedded contracts declare, so a bump in lightwave-core fails
// here until the Go code that hard-codes those semantics is updated.
//
// This is the test that would have failed the day CORE-0051 merged. Instead
// worktree_policy moved to v2.0.0, lw kept enforcing v1.1.0, and `lw git audit`
// reported the stamp backwards across 48 checkouts until someone read both
// documents by hand (lightwave-cli#380).
//
// If this fails: read the contract, update the Go code that implements it, then
// move the pin. Moving the pin alone reports agreement that does not exist.
func TestCheckStamp_PinnedContractsMatchTheEmbeddedStamp(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, implementedContractVersions,
		"an empty pin table is a check that cannot fail")

	findings := contractVersionFindings(implementedContractVersions)

	for _, f := range findings {
		t.Errorf("[%s] %s", f.Code, f.Message)
	}
}

// TestCheckStamp_FiresOnAWrongPin is the known-bad fixture: a pin that does not
// match the embedded contract must produce a finding naming both versions.
func TestCheckStamp_FiresOnAWrongPin(t *testing.T) {
	t.Parallel()

	const contract = "policy/security/worktree_policy"

	actual, err := declaredContractVersion(contract)
	require.NoError(t, err, "the embedded contract must be readable for this fixture")

	findings := contractVersionFindings(map[string]string{contract: "0.0.1-not-the-real-version"})

	require.Len(t, findings, 1, "exactly one contract was pinned wrongly")
	assert.Equal(t, "contract_version_drift", findings[0].Code)
	assert.Equal(t, contract, findings[0].Contract)
	assert.Equal(t, "0.0.1-not-the-real-version", findings[0].Expected)
	assert.Equal(t, actual, findings[0].Actual,
		"the finding must report what the contract actually declares")
	assert.Contains(t, findings[0].Message, actual,
		"the message must name the real version so the reader can act without rerunning anything")
}

// TestCheckStamp_StaysSilentOnAMatchingPin is the known-good fixture: a pin
// taken from the embedded contract itself must produce nothing.
func TestCheckStamp_StaysSilentOnAMatchingPin(t *testing.T) {
	t.Parallel()

	const contract = "policy/security/worktree_policy"

	actual, err := declaredContractVersion(contract)
	require.NoError(t, err)

	findings := contractVersionFindings(map[string]string{contract: actual})

	assert.Empty(t, findings, "a pin equal to the declared version is not drift")
}

// TestCheckStamp_UnreadableContractIsAFindingNotSilence guards the failure mode
// this whole check exists to avoid: a contract that cannot be read must be
// reported, never treated as agreement.
func TestCheckStamp_UnreadableContractIsAFindingNotSilence(t *testing.T) {
	t.Parallel()

	findings := contractVersionFindings(map[string]string{
		"policy/security/there_is_no_such_contract": "1.0.0",
	})

	require.Len(t, findings, 1)
	assert.Equal(t, "contract_unreadable", findings[0].Code)
	assert.Contains(t, findings[0].Message, "there_is_no_such_contract")
}

func TestCheckStamp_DeclaredContractVersionReadsMeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contract string
		wantErr  string
	}{
		{name: "real contract resolves", contract: "policy/security/worktree_policy"},
		{name: "missing contract errors", contract: "policy/security/nope", wantErr: "not found"},
	}

	for _, testCase := range tests {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := declaredContractVersion(tt.contract)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got, "a contract with no version cannot be compared")
		})
	}
}

// TestCheckStamp_ResultErrorMapsToTheExitConvention pins AGENTS.md's contract:
// 0 clean, non-zero when findings exist.
func TestCheckStamp_ResultErrorMapsToTheExitConvention(t *testing.T) {
	t.Parallel()

	require.NoError(t, stampResultError(0), "no findings must exit clean")
	require.Error(t, stampResultError(1), "a finding must not exit 0 — that is the whole defect")
}
