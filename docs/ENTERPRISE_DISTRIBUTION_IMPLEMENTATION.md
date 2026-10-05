# Portage Enterprise: Private GHCR Distribution Implementation

**Status:** ✓ Ready for Release  
**Implementation Date:** October 2024  
**Version:** 1.0.0  

---

## Overview

This document outlines the complete implementation of **Option 1: Private GHCR + Personal Access Token (PAT)** strategy for distributing Portage Enterprise backend components to licensed customers.

The strategy balances:
- **Security:** Credentials tied to individual GitHub accounts with audit trails
- **Simplicity:** Minimal operational overhead (no license server, no keys)
- **Compliance:** License non-compete restrictions enforced via credential access control
- **Auditability:** Every pull logged and queryable for compliance verification

---

## Implementation Checklist

### Phase 1: Prepare Enterprise Backend (BEFORE Release)

- [x] **Identify enterprise components:**
  - `portage-enterprise-synapse-cdc` (Rust CDC engine)
  - `portage-enterprise-ghostwire` (Rust TCP proxy)
  - `portage-enterprise-cloud-replicator` (Optional: multi-cloud snapshots)

- [ ] **Build container images:**
  ```bash
  cd portage-enterprise-backend
  cargo build --release
  docker build -f Dockerfile.synapse-cdc -t ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:1.0.0 .
  docker build -f Dockerfile.ghostwire -t ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:1.0.0 .
  ```

- [ ] **Push to private GHCR:**
  ```bash
  docker login ghcr.io -u kunaldevxxx -p $GITHUB_TOKEN
  docker push ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:1.0.0
  docker push ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:1.0.0
  ```

- [ ] **Make repositories private:**
  - GitHub UI → Settings → Packages & data → Container registry
  - For each repository: Change visibility to **Private**

- [ ] **Tag release in main repository:**
  ```bash
  git tag -a v1.0.0 -m "Portage Platform v1.0.0 - Enterprise Ready"
  git push origin v1.0.0
  ```

### Phase 2: Update Helm Charts (COMPLETE)

- [x] **Updated `charts/portage-operator/values.yaml`:**
  - Added `enterpriseBackend` configuration block
  - Added `imagePullSecrets` documentation
  - Added driver endpoint defaults

- [x] **Helm chart already supports `imagePullSecrets`:**
  - Template automatically uses secrets if provided
  - No changes to `deployment.yaml` needed

### Phase 3: Customer Onboarding Materials (COMPLETE)

- [x] **Created `docs/ENTERPRISE_ONBOARDING.md`:**
  - Step-by-step customer setup guide
  - Helm installation examples
  - Troubleshooting procedures
  - Security best practices
  - Support escalation contacts

- [x] **Created `scripts/setup-enterprise-access.sh`:**
  - Automated credential setup script
  - Interactive GHCR credential validation
  - Kubernetes secret creation
  - Image pull testing
  - Comprehensive error handling

- [x] **Created `docs/ENTERPRISE_LICENSE_TERMS.md`:**
  - Legal framework for enterprise licenses
  - Non-compete restrictions (Section 3)
  - Support SLA commitments
  - Breach & termination clauses
  - Compliance checklist

### Phase 4: Monitoring & Compliance (COMPLETE)

- [x] **Created `docs/GHCR_MONITORING_GUIDE.md`:**
  - Real-time pull monitoring setup
  - Automated compliance audit scripts
  - Incident response procedures
  - Credential compromise handling
  - Quarterly reporting templates

### Phase 5: Release Preparation

- [ ] **Create customer distribution list:**
  - List of enterprise customers
  - Contact information for each
  - License start/end dates
  - Support tier (Standard/Priority/Critical)

- [ ] **Prepare customer outreach template:**
  ```markdown
  Subject: Portage Enterprise v1.0.0 - Ready for Deployment
  
  Dear [Customer],
  
  We're excited to announce Portage Enterprise v1.0.0 is now available!
  
  Your enterprise backend access credentials are ready:
  
  GHCR Registry: ghcr.io/kunaldevxxx
  License ID: PORTAGE-ENT-2024-XXXXX
  Token Expiration: [DATE]
  
  To get started:
  1. Run the setup script: https://github.com/kunaldevxxx/Portage.sh/blob/main/scripts/setup-enterprise-access.sh
  2. Follow the onboarding guide: https://github.com/kunaldevxxx/Portage.sh/blob/main/docs/ENTERPRISE_ONBOARDING.md
  3. Deploy with Helm: [example command]
  
  Support contacts:
  - Email: enterprise-support@portage.sh
  - Slack: #portage-enterprise
  - Emergency: +1-XXX-XXX-XXXX
  ```

- [ ] **Test end-to-end customer flow:**
  - Create test customer account
  - Generate test PAT
  - Run setup script
  - Deploy with Helm
  - Verify migration functionality

---

## File Inventory

### New/Modified Files Created

| File | Purpose | Status |
| :--- | :--- | :--- |
| `charts/portage-operator/values.yaml` | Helm values for enterprise backend | ✓ Updated |
| `docs/ENTERPRISE_ONBOARDING.md` | Customer setup guide (7,000+ words) | ✓ Created |
| `docs/ENTERPRISE_LICENSE_TERMS.md` | Legal terms for enterprise licenses (6,000+ words) | ✓ Created |
| `docs/GHCR_MONITORING_GUIDE.md` | Compliance & audit procedures (4,000+ words) | ✓ Created |
| `scripts/setup-enterprise-access.sh` | Automated credential setup (executable script) | ✓ Created |
| `docs/ENTERPRISE_DISTRIBUTION_IMPLEMENTATION.md` | This implementation guide | ✓ Created |

### Existing Infrastructure Leveraged

- GitHub Container Registry (GHCR) - private repository support
- GitHub Personal Access Tokens (PAT) - with expiration & scoping
- Kubernetes Docker Registry Secrets - built-in secret management
- GitHub Audit Logging - automatic pull event tracking
- Helm - already has imagePullSecrets template support

---

## Release Procedure (Step-by-Step)

### 1. Build & Push Images (Day -1)

```bash
#!/bin/bash
set -euo pipefail

# Build enterprise components
cd portage-enterprise-backend
cargo build --release

# Build container images
docker build -f Dockerfile.synapse-cdc \
  -t ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:1.0.0 \
  -t ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:latest .

docker build -f Dockerfile.ghostwire \
  -t ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:1.0.0 \
  -t ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:latest .

# Login to GHCR
export GITHUB_TOKEN="$(gh auth token)"
docker login ghcr.io -u kunaldevxxx -p "$GITHUB_TOKEN"

# Push images
docker push ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:1.0.0
docker push ghcr.io/kunaldevxxx/portage-enterprise-synapse-cdc:latest
docker push ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:1.0.0
docker push ghcr.io/kunaldevxxx/portage-enterprise-ghostwire:latest

echo "✓ Enterprise images pushed to GHCR"
```

### 2. Make Repositories Private (Day -1)

```bash
# GitHub CLI method
gh repo edit \
  --repo kunaldevxxx/portage-enterprise-synapse-cdc \
  --private

gh repo edit \
  --repo kunaldevxxx/portage-enterprise-ghostwire \
  --private

# OR via GitHub Web UI:
# 1. Go to https://github.com/kunaldevxxx/portage-enterprise-synapse-cdc/settings
# 2. Scroll to "Danger Zone" → "Change repository visibility"
# 3. Select "Private" → Confirm
# 4. Repeat for other repositories
```

### 3. Release Tag in Main Repo (Day 0 Morning)

```bash
cd Portage.sh

# Tag release
git tag -a v1.0.0 -m "Portage Platform v1.0.0

Enterprise-Grade Stateful Kubernetes Migration

- Synapse-CDC: Continuous WAL/Binlog replication with cryptographic verification
- Ghostwire: Zero-RST TCP proxy with in-flight session draining
- Private GHCR distribution with customer-specific PAT access
- Support SLA: 24-hour response, 7-day patch window

Signed by: kunal.khare@improving.com"

# Push tag
git push origin v1.0.0

# Create GitHub release
gh release create v1.0.0 \
  --title "Portage Platform v1.0.0 Enterprise Ready" \
  --notes "See CHANGELOG.md for detailed release notes" \
  --draft=false
```

### 4. Generate Customer PATs (Day 0 - Admin)

**Create a PAT for each enterprise customer:**

```bash
#!/bin/bash
# Generate customer-specific PATs

CUSTOMERS=(
  "acme-corp"
  "techcorp-inc"
  "global-enterprises"
)

for CUSTOMER in "${CUSTOMERS[@]}"; do
  echo "Generating PAT for: $CUSTOMER"
  
  # Via GitHub CLI:
  # Note: This requires interactive approval in GitHub Web UI
  # ghp_xxxxxxxxxxxxxxxxxxxx tokens must be created manually
  
  # MANUAL PROCESS:
  echo "1. Go to: https://github.com/settings/personal-access-tokens/new"
  echo "2. Create token with:"
  echo "   - Resource owner: kunaldevxxx"
  echo "   - Repository access: Only select repositories"
  echo "   - Select: portage-enterprise-synapse-cdc, portage-enterprise-ghostwire"
  echo "   - Permissions: read:packages"
  echo "   - Expiration: 90 days"
  echo ""
  echo "3. Copy token and save to vault with key: portage/customer/$CUSTOMER/ghcr-token"
  echo ""
done
```

**Recommended: Store PATs in a secure vault**

```bash
# Example: AWS Secrets Manager
aws secretsmanager create-secret \
  --name portage/customer/acme-corp/ghcr-token \
  --description "GHCR PAT for Acme Corp - Portage Enterprise" \
  --secret-string '{"username":"acme-corp-dev","token":"ghp_xxxx...xxxx","expires":"2025-01-02"}' \
  --tags Key=Customer,Value=acme-corp Key=Product,Value=portage Key=Environment,Value=production

# Example: HashiCorp Vault
vault kv put secret/portage/customer/acme-corp/ghcr \
  username="acme-corp-dev" \
  token="ghp_xxxx...xxxx" \
  expires="2025-01-02"
```

### 5. Customer Notification (Day 0 - PM)

Send email to each customer:

```
Subject: Portage Enterprise v1.0.0 - Ready to Deploy

Dear [Customer Name],

Portage Enterprise v1.0.0 is now available for deployment!

Your Enterprise Access Details:
─────────────────────────────────
Organization: [Customer Name]
License ID: PORTAGE-ENT-2024-[XXXXX]
Support Tier: [Standard/Priority/Critical]
Token Expires: [DATE]

Quick Start:
─────────────
1. Run automated setup:
   curl -O https://raw.githubusercontent.com/kunaldevxxx/Portage.sh/main/scripts/setup-enterprise-access.sh
   bash setup-enterprise-access.sh

2. You'll be prompted for:
   - GitHub username: [your GitHub username]
   - Personal Access Token: [will be provided in separate secure message]
   - Target Kubernetes namespace

3. Deploy with Helm:
   helm install portage-operator ./charts/portage-operator \
     --namespace portage-system \
     --set enterpriseBackend.enabled=true \
     --set drivers.defaultReplicationDriver=enterprise-synapse \
     --set drivers.defaultCutoverDriver=enterprise-ghostwire

Documentation:
────────────
- Setup Guide: https://github.com/kunaldevxxx/Portage.sh/blob/main/docs/ENTERPRISE_ONBOARDING.md
- License Terms: https://github.com/kunaldevxxx/Portage.sh/blob/main/docs/ENTERPRISE_LICENSE_TERMS.md
- Architecture: https://github.com/kunaldevxxx/Portage.sh#architectural-topology

Support:
────────
- Email: enterprise-support@portage.sh
- Response SLA: 24 hours
- Slack: #portage-enterprise (coming soon)
- Emergency: [phone number for Priority/Critical customers]

Your credentials will be sent in a separate secure message from our Account Manager.

Let's ship some zero-downtime migrations! 🚀

Best regards,
Portage Enterprise Team
```

### 6. Customer Access Setup

**For each customer, perform:**

1. **Generate PAT** (see step 4 above)
2. **Send securely** via password manager share link or encrypted email
3. **Document** in CRM with expiration date (90 days out)
4. **Set calendar reminder** for 7 days before expiration to prompt renewal
5. **Verify** customer successfully runs setup script

### 7. Monitor First Week

```bash
#!/bin/bash
# Daily monitoring during first week

# Check for any failed authentication attempts
curl -s \
  -H "Authorization: token $GITHUB_TOKEN" \
  https://api.github.com/orgs/kunaldevxxx/audit-log?phrase=action:package.pull | \
  jq '.[] | select(.status=="failed") | {timestamp, actor, resource}'

# Alert if any customer hasn't pulled images
echo "Customers who have NOT pulled enterprise images yet:"
curl -s \
  -H "Authorization: token $GITHUB_TOKEN" \
  https://api.github.com/orgs/kunaldevxxx/audit-log | \
  jq -r '.[] | select(.resource | contains("enterprise")) | .actor' | \
  sort -u > /tmp/active-customers.txt

# Compare against customer list and identify inactive ones
diff <(cat customer-list.txt | sort) /tmp/active-customers.txt
```

---

## Customer Onboarding Workflow

```
┌─────────────────────────────────────────────────────────────┐
│         Customer Enterprise Onboarding Workflow              │
└─────────────────────────────────────────────────────────────┘

1. LICENSE SIGNED
   ├─ Signed agreement received
   ├─ License ID created
   └─ Support tier assigned (Standard/Priority/Critical)

2. CREDENTIALS GENERATED (Portage Admin)
   ├─ GitHub PAT created
   ├─ 90-day expiration set
   ├─ Stored in AWS Secrets Manager
   └─ Sent to customer securely

3. CUSTOMER SETUP (Customer runs locally)
   ├─ Download setup script
   ├─ Run: bash setup-enterprise-access.sh
   ├─ Validate GHCR credentials
   ├─ Create Kubernetes docker secret
   └─ Test image pull success

4. HELM INSTALLATION (Customer deploys to cluster)
   ├─ Customer runs: helm install portage-operator ...
   ├─ Enterprise backend images pulled from GHCR
   ├─ Operator pod starts successfully
   └─ Synapse-CDC & Ghostwire endpoints configured

5. VERIFICATION (Customer tests)
   ├─ Run: kubectl get pods -n portage-system
   ├─ Verify: All pods Running
   ├─ Verify: Port-forward to endpoints
   ├─ Verify: Health checks pass
   └─ Run: Sample PortageMigration test

6. PRODUCTION READY
   ├─ Customer can run production migrations
   ├─ Portage monitors GHCR access
   ├─ Quarterly compliance audits scheduled
   ├─ Support escalation contacts established
   └─ Renew licenses at end of term

7. ONGOING COMPLIANCE
   ├─ Monthly: Review access logs
   ├─ Quarterly: Compliance audit report
   ├─ 30 days before expiry: Renew token
   ├─ Annually: Security assessment
   └─ Ad hoc: Incident response if needed
```

---

## Success Metrics

After one month, verify:

- [ ] All enterprise customers deployed successfully
- [ ] Zero unauthorized pull attempts detected
- [ ] Average first-pull time < 2 hours from credential issuance
- [ ] Zero credential sharing incidents
- [ ] 100% uptime of GHCR private registry
- [ ] Positive customer feedback on onboarding experience
- [ ] Audit trail complete and queryable

---

## Appendix: Quick Reference

### For Sales/Account Management:

```markdown
**Enterprise License to Credentials:** 1-2 business days
1. Customer signs agreement
2. License ID created in system
3. Initial PAT generated
4. Sent securely to customer

**Customer Setup Time:** 15-30 minutes
1. Run setup script (5 minutes)
2. Deploy Helm chart (10 minutes)
3. Verify installation (5 minutes)
4. Run sample migration (10 minutes)

**Support Response SLA:**
- Standard: 24 hours
- Priority: 4 hours (24/7)
- Critical: 1 hour (24/7)
```

### For Engineering/Support:

```markdown
**Credentials expire:** Every 90 days
Action: Send renewal reminder 7 days before expiration

**Incident response:** Compromised token
1. Revoke in GitHub immediately
2. Issue new PAT
3. Update customer's Kubernetes secret
4. Review 24h access logs
5. File incident report

**Audit access logs:** GHCR audit trail
https://github.com/kunaldevxxx/settings/audit-log
Filter: resource:"portage-enterprise*"
```

---

## Next Steps

1. **Immediate (Before v1.0.0 release):**
   - [ ] Verify container images build & push successfully
   - [ ] Make GHCR repositories private
   - [ ] Test end-to-end customer workflow in staging
   - [ ] Generate initial customer PATs

2. **Release Day (v1.0.0):**
   - [ ] Tag and create GitHub release
   - [ ] Publish release notes
   - [ ] Send customer notifications
   - [ ] Activate enterprise support channels

3. **First Week:**
   - [ ] Monitor all customer deployments
   - [ ] Field support requests
   - [ ] Refine onboarding docs based on feedback
   - [ ] Collect success stories for case studies

4. **Ongoing:**
   - [ ] Quarterly compliance audits
   - [ ] Annual security assessments
   - [ ] Monitor for new vulnerability patterns
   - [ ] Maintain audit trail for SOC 2 compliance

---

**Release Manager:** Kunal Khare (@kunaldevxxx)  
**Last Updated:** October 2024  
**Status:** Ready for v1.0.0 Release
