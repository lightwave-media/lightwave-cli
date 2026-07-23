# github-org-sync: GitHub App credentials

The scheduled `github-org-sync` workflow checks out private sibling
`lightwave-infrastructure-catalog`, labels repos across the org, and
reconciles the Lightwave Swarm project board. The default `GITHUB_TOKEN` is
scoped to the repo running the job, so sibling checkout and org-wide writes
fail with REST `Not Found` / permission errors.

Use a **GitHub App** (same approach as `docs/release-tap-app.md` for the
Homebrew tap), not a long-lived PAT.

## One-time setup

### 1. Create the App (org-owned)

<https://github.com/organizations/lightwave-media/settings/apps/new>

- **Name:** `lightwave-org-sync`
- **Homepage URL:** `https://github.com/lightwave-media/lightwave-cli`
- **Webhook:** uncheck **Active**
- **Repository permissions:**
  - Contents: Read-only
  - Issues: Read and write
  - Metadata: Read-only
  - Pull requests: Read-only
- **Organization permissions:**
  - Projects: Read and write
- **Where can this App be installed:** Only on this account

Create, note the **App ID**, then **Generate a private key** (downloads a `.pem`).

### 2. Install on the swarm repos

App page → **Install App** → `lightwave-media` → select the repositories
listed in `.github/workflows/github-org-sync.yml` (or **All repositories** if
you prefer less churn when the swarm list grows).

### 3. Store secrets on lightwave-cli

```bash
gh secret set ORG_SYNC_APP_ID --repo lightwave-media/lightwave-cli --body "<app-id>"
gh secret set ORG_SYNC_APP_PRIVATE_KEY --repo lightwave-media/lightwave-cli < /path/to/key.pem
```

### 4. Re-enable the workflow

```bash
gh api --method PUT \
  repos/lightwave-media/lightwave-cli/actions/workflows/github-org-sync.yml/enable
gh workflow run github-org-sync.yml --repo lightwave-media/lightwave-cli
```

Duplicate `github-org-sync.yml` copies on other repos (core, catalog, ai) are
intentionally disabled — canonical runner is **lightwave-cli** only.
