# Enterprise Repository Security & Branch Protection Policy

To guarantee the integrity of air-gapped Fortune 500 deployments and prevent unauthorized modifications, this repository enforces **Zero Direct Pushes** to production branches (`main` / `master`).

---

## 1. Branch Protection Rules Summary

All changes must pass through a Pull Request subject to the following gates:

| Policy Control | Setting | Enforcement Reason |
| :--- | :--- | :--- |
| **Direct Commits** | **Disabled** | Nobody (including administrators) can push directly to `main`. |
| **Required PR Reviews** | **Enabled (Min: 1)** | Every PR must be reviewed and approved. |
| **CODEOWNERS Approval** | **Mandatory** | Review MUST be approved by `@kunaldevxxx` specified in `.github/CODEOWNERS`. |
| **Dismiss Stale Approvals** | **Enabled** | When new commits are pushed, existing approvals are invalidated. |
| **Require Linear History** | **Enabled** | Only clean rebase or squash merges allowed; no merge commits. |
| **Require Signed Commits** | **Enabled** | All commits must be cryptographically signed with GPG or SSH keys. |
| **Require Status Checks** | **Strict** | CI test suites, security scans, and lint checks must be green. |
| **Prevent Force Pushes** | **Enabled** | `--force` pushes are completely blocked on protected branches. |
| **Prevent Deletions** | **Enabled** | Branch deletion is restricted. |

---

## 2. GitHub Ruleset Configuration (Step-by-Step)

Repository owners can apply this configuration directly in GitHub Settings:

1. Navigate to: **Settings** $\rightarrow$ **Rules** $\rightarrow$ **Rulesets** $\rightarrow$ **New branch ruleset**.
2. **Ruleset Name:** `Enterprise Main Protection`
3. **Enforcement status:** `Active`
4. **Target branches:**
   - Click **Add target** $\rightarrow$ **Include default branch** (`main`).
5. **Rules to Enable:**
   - [x] **Restrict creations**
   - [x] **Restrict updates**
   - [x] **Restrict deletions**
   - [x] **Require linear history**
   - [x] **Require signed commits**
   - [x] **Require a pull request before merging:**
     - Required approvals: `1`
     - [x] **Require review from Code Owners**
     - [x] **Dismiss stale pull request approvals when new commits are pushed**
     - [x] **Require conversation resolution before merging**
   - [x] **Require status checks to pass:**
     - `go-ci / test`
     - `rust-ci / test`
     - `security / trivy-scan`
     - `security / gitleaks`

---

## 3. GitHub CLI Automated Configuration

If you have the GitHub CLI (`gh`) installed with admin permissions on the repository, run:

```bash
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  /repos/kunaldevxxx/Portage.sh/branches/main/protection \
  --input - << 'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "go-ci / test",
      "rust-ci / test",
      "security / gitleaks",
      "security / trivy-scan"
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": true,
    "required_approving_review_count": 1
  },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "block_creations": true,
  "required_conversation_resolution": true
}
JSON
```
