# macOS Notarization — Activation Runbook

`lw` ships pre-built darwin binaries via the Homebrew tap. Apple's
Gatekeeper distinguishes:

- **adhoc-signed** binaries — what `goreleaser` produces by default.
  Each first-launch triggers a `syspolicyd` reputation check that can
  take 10+ seconds — and on `/opt/homebrew/bin/*` (a trusted prefix)
  it can hang indefinitely. CLAUDE.md documents this as the reason
  hand-overwriting the tap binary breaks every `lw`-invoking hook.

- **Developer ID Application + notarized** binaries — pre-approved by
  Apple. No reputation check at first launch, no hang.

This repo has the **infrastructure** for the notarized path wired in,
but it's dormant until the secrets below land. This runbook flips it
on.

---

## What's already in place

| File | Purpose |
|---|---|
| `.gon_amd64.hcl` | gon config for darwin/amd64 (bundle_id, signing identity, output path) |
| `.gon_arm64.hcl` | gon config for darwin/arm64 |
| `.github/workflows/sign-macos.yml` | Reusable workflow: downloads goreleaser darwin artifacts → imports cert → runs gon → verifies non-adhoc signature → uploads signed zips to the GitHub release |
| `.github/workflows/release.yml` | `sign-macos:` job is **commented out**; uncomment to activate |

---

## Activation steps

### 1. Apple Developer account + Developer ID Application certificate

You need an active Apple Developer Program membership (currently $99/yr).
From <https://developer.apple.com/account/resources/certificates/list>:

1. Create a new certificate, type "Developer ID Application".
2. Generate the CSR locally (`Keychain Access → Certificate Assistant →
   Request a Certificate from a Certificate Authority`).
3. Upload the CSR, download the resulting `.cer`.
4. Double-click to install into your local keychain.
5. Export the private-key + cert as a `.p12` file (Keychain Access →
   right-click the identity → Export). Set a strong export password —
   you'll need it in step 4.

### 2. App-specific password OR API key

Notarization needs to authenticate as your Apple ID. Two options:

**Option A — App-specific password** (simpler, what these configs use):

1. <https://appleid.apple.com/account/manage> → Sign-In and Security →
   App-Specific Passwords.
2. Generate one labeled "lightwave-cli notarization".
3. Save it; you'll add it as `MACOS_AC_PASSWORD`.

**Option B — API key** (rotation-friendly, recommended long-term):

1. <https://appstoreconnect.apple.com/access/users> → Keys tab.
2. Generate an API key with "Developer" role.
3. Download the `.p8` private key file.
4. Note the Key ID and Issuer ID.

If you go with option B, the `.gon_*.hcl` files need a different
`apple_id { … }` shape — see <https://github.com/mitchellh/gon#api-key>.

### 3. Find your Apple Team ID (provider)

```
xcrun altool --list-providers -u <your-apple-id> -p <app-specific-password>
```

The output column `ProviderShortname` is the Team ID. Add as `MACOS_AC_PROVIDER`.

### 4. Add the five repo secrets

In the lightwave-cli repo, **Settings → Secrets and variables → Actions
→ New repository secret**:

| Secret name | Value |
|---|---|
| `MACOS_CERTIFICATE` | Base64 of the `.p12` from step 1: `base64 -i cert.p12 \| pbcopy` |
| `MACOS_CERTIFICATE_PASSWORD` | The export password from step 1 |
| `MACOS_AC_LOGIN` | Your Apple ID email |
| `MACOS_AC_PASSWORD` | The app-specific password from step 2 |
| `MACOS_AC_PROVIDER` | Your Team ID from step 3 |

### 5. Verify the signing identity in the HCL files

`.gon_amd64.hcl` and `.gon_arm64.hcl` both declare:

```hcl
sign {
  application_identity = "Developer ID Application: LightWave Media, LLC"
}
```

That string must exactly match the certificate's Common Name. List your
local identities to confirm:

```
security find-identity -v -p codesigning
```

If the certificate is registered to a different name (e.g.
`Joel Schaeffer`), update both `.gon_*.hcl` files.

### 6. Uncomment the `sign-macos` job in `release.yml`

Edit `.github/workflows/release.yml`, find the block:

```yaml
  # sign-macos:
  #   needs: [release]
  #   uses: ./.github/workflows/sign-macos.yml
  #   secrets: inherit
  #   with:
  #     tag: ${{ inputs.tag || github.ref_name }}
```

Remove the leading `# ` from each line. Commit + push as its own PR
("chore(release): enable macOS notarization") so the activation is
reviewable.

### 7. Cut a test release

```
git tag v0.0.0-notarize-test
git push origin v0.0.0-notarize-test
```

Watch the Actions tab. The `release` job builds; `sign-macos` then
downloads the darwin artifacts, signs + notarizes, and uploads the
signed `.zip`s to the GitHub release.

### 8. Verify the signed binary

Download `lw_darwin_arm64_signed.zip` from the release, unzip, and
inspect:

```
codesign -dv --verbose=4 lw
# Expected:
#   Identifier=io.lightwave-media.cli
#   Authority=Developer ID Application: <your name>
#   TeamIdentifier=<your team ID>
#   Sealed Resources=... (NOT "adhoc")
```

If it reports `Signature=adhoc`, gon ran but the cert wasn't found —
re-check step 5.

If it reports a valid identity, the notarized binary is good. Delete
the test tag (`gh release delete v0.0.0-notarize-test && git push
origin :v0.0.0-notarize-test`) and cut the next real release.

---

## Rotating secrets

App-specific passwords don't expire but should rotate yearly. To
rotate:

1. Generate a new app-specific password at
   <https://appleid.apple.com/account/manage>.
2. Update the `MACOS_AC_PASSWORD` repo secret with the new value.
3. Revoke the old password from the same page.

The Developer ID Application certificate expires every 5 years. To
renew, repeat step 1 of activation — exporting a fresh `.p12` and
updating the `MACOS_CERTIFICATE` + `MACOS_CERTIFICATE_PASSWORD`
secrets.

---

## When this isn't worth doing

- If `lw` is only ever installed via `brew install lightwave-media/tap/lw`
  AND Homebrew's quarantine handling matures to skip `syspolicyd` on
  tap-installed binaries, the hang goes away even with adhoc-signed
  binaries. Worth re-evaluating once macOS 16+ ships if Homebrew's
  formula behavior changes.
- If `lw` distribution moves entirely to a private S3 bucket or other
  non-public channel, Gatekeeper requirements relax.

For the current shape (public homebrew-tap, public GitHub releases),
notarization is the standard answer.
