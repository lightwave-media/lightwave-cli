# Release: Homebrew-tap push via GitHub App

GoReleaser publishes the `lw` formula to `lightwave-media/homebrew-tap` on
every tagged release. Pushing to a *different* repo than the one running CI
needs a credential the built-in `GITHUB_TOKEN` can't provide (it's scoped to
`lightwave-cli` only).

We use a **GitHub App** for this, not a Personal Access Token. The App mints
a short-lived installation token at runtime (`actions/create-github-app-token`
in `release.yml`), so:

- nothing expires — no recurring 401s when a PAT lapses (the failure mode
  that stalled v3.0.0 and v3.1.0 on 2026-06-12);
- the formula commit is authored by the app bot, not a human;
- access is scoped to `homebrew-tap` Contents and nothing else.

## One-time setup

### 1. Create the App (org-owned)

<https://github.com/organizations/lightwave-media/settings/apps/new>

- **Name:** `lightwave-tap-publisher`
- **Homepage URL:** the lightwave-cli repo URL (any valid URL is fine)
- **Webhook:** uncheck **Active** (this App takes no events)
- **Repository permissions → Contents:** Read and write (leave everything
  else "No access")
- **Where can this App be installed:** Only on this account
- Create, then on the App's page note the **App ID** and click
  **Generate a private key** (downloads a `.pem`).

### 2. Install it on the tap

On the App's page → **Install App** → install on `lightwave-media`, **Only
select repositories → `homebrew-tap`**.

### 3. Store the two secrets on lightwave-cli

```bash
gh secret set TAP_APP_ID --repo lightwave-media/lightwave-cli --body "<app-id>"
gh secret set TAP_APP_PRIVATE_KEY --repo lightwave-media/lightwave-cli < /path/to/key.pem
```

### 4. Remove the dead PAT

```bash
gh secret delete HOMEBREW_TAP_TOKEN --repo lightwave-media/lightwave-cli
```

## How a release uses it

`release.yml` runs `actions/create-github-app-token@v2` with `owner:
lightwave-media` and `repositories: homebrew-tap`, then passes
`steps.tap-token.outputs.token` to GoReleaser as `HOMEBREW_TAP_TOKEN`. The
token lives only for that job.

The `.goreleaser.yaml` `brews.repository.token` reference
(`{{ .Env.HOMEBREW_TAP_TOKEN }}`) is unchanged — only the *source* of that
env var moved from a static secret to the per-run App token.
