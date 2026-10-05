# Portage Enterprise: License Terms & Conditions

**Effective Date:** October 2024  
**Version:** 1.0  
**For Enterprise License Holders Only**

---

## Overview

This document outlines the terms and conditions for using the Portage Enterprise Backend Components (Synapse-CDC, Ghostwire, Cloud Replicator) in production environments. 

**Base License:** Business Source License 1.1 (BSL 1.1)  
**License Type:** Non-Compete Commercial License  
**Change Date:** October 1, 2028 (converts to Apache 2.0)

---

## 1. Definitions

- **"Licensed Work":** Portage Platform, including Portage Operator (Go), Synapse-CDC, Ghostwire, and Cloud Replicator components.
- **"Enterprise Components":** Private, proprietary backend drivers accessed via private GHCR registry: `enterprise-synapse`, `enterprise-ghostwire`, `enterprise-cloud-replicator`.
- **"Licensee":** The organization that has purchased an active Portage Enterprise license.
- **"Affiliate":** Any entity controlling, controlled by, or under common control with Licensee.
- **"Permitted Use":** Internal business operations within Licensee's organization for database and stateful workload migration.
- **"Prohibited Use":** Any use that violates Section 3 below.

---

## 2. Grant of Rights

Licensor hereby grants Licensee a **non-exclusive, non-sublicensable, non-transferable** license to:

1. **Download & Deploy:** Pull enterprise components from private GHCR (`ghcr.io/kunaldevxxx/portage-enterprise-*`) using provided credentials.
2. **Execute:** Run enterprise components in Licensee's Kubernetes clusters for internal use.
3. **Distribute:** Deploy to Licensee's infrastructure and Affiliates for internal operations only.
4. **Documentation:** Use provided documentation, runbooks, and architecture guides internally.
5. **Support:** Receive vendor support, security patches, and updates per the support SLA.

---

## 3. CRITICAL Non-Compete Restrictions (Compliance Required)

Licensee **SHALL NOT** and explicitly covenants to refrain from:

### 3.1 Prohibited Commercial Services
- Offering Portage or derivative migrations as a **hosted service, managed service, SaaS, platform-as-a-service (PaaS), or database-as-a-service (DBaaS)** to third parties.
- Competing with Licensor by providing stateful container or database migration offerings incorporating Portage technology.
- Embedding or integrating Enterprise Components into products or services offered to customers, clients, or external parties.

**Example Violations:**
- ❌ "Migration as a Service" offering that uses Portage enterprise backend
- ❌ SaaS platform with built-in Portage database migration
- ❌ Managed Kubernetes migration tool reselling Portage features
- ✓ Internal use: migrating your own production databases
- ✓ Affiliate use: helping subsidiary migrate stateful workloads

### 3.2 Credential & Secret Abuse
- **Sharing GHCR credentials** with unauthorized organizations, customers, or external parties.
- **Redistributing container images** beyond Licensee's internal infrastructure.
- **Committing credentials to git repositories**, CI/CD systems, or public sources.
- **Circumventing access controls** via image tagging, re-hosting, or mirror registries.

**Compliance Requirements:**
- Credentials must be **stored in secure vaults** (AWS Secrets Manager, HashiCorp Vault, 1Password, etc.).
- Access is **logged and auditable** by Licensor at any time.
- Token rotation is **required every 90 days**.
- Unauthorized pull attempts trigger **automatic investigation and potential license termination**.

### 3.3 Code Modification & Reverse Engineering
- **Decompiling, reverse engineering, or disassembling** enterprise components.
- **Modifying binaries or container images** without explicit written consent.
- **Extracting or reproducing** Synapse-CDC's Merkle tree verification or Ghostwire's packet-splice logic.
- **Creating derivative works** without a separate commercial license agreement.

---

## 4. License Compliance Audits

Licensor reserves the right to:

1. **Audit Registry Access:** Review GHCR pull logs for unauthorized usage patterns.
2. **Inspect Deployments:** Request evidence of proper credential management and license compliance.
3. **Verify Usage Scope:** Confirm enterprise components are only deployed in authorized clusters.
4. **Security Assessments:** Conduct security reviews to ensure credential isolation.

**Audit Frequency:** At Licensor's discretion (typically annual for compliant customers).

---

## 5. Support & SLA Commitments

### 5.1 Enterprise Support Tiers

| Tier | Response Time | Patch SLA | Availability | Cost |
| :--- | :--- | :--- | :--- | :--- |
| **Standard** | 24 hours | 7 days | Business hours | Included |
| **Priority** | 4 hours | 48 hours | 24/7/365 | +$X,XXX/year |
| **Critical** | 1 hour | 24 hours | 24/7/365 | +$X,XXX/year |

### 5.2 Security Patch Policy
- **Critical vulnerabilities** (CVSS 9.0+): Patch within 24-48 hours
- **High severity** (CVSS 7.0-8.9): Patch within 7 days
- **Medium severity** (CVSS 4.0-6.9): Patch within 30 days
- **Low severity** (CVSS < 4.0): Best effort

### 5.3 Support Channels
- **Email:** enterprise-support@portage.sh
- **Slack:** #portage-enterprise (for Standard/Priority/Critical SLA customers)
- **Phone:** +1-XXX-XXX-XXXX (Critical SLA only)
- **GitHub:** Private Security Advisories (https://github.com/kunaldevxxx/Portage.sh/security)

---

## 6. Termination & Breach Consequences

### 6.1 License Termination
License automatically terminates upon:
- **Non-Payment:** 30 days past invoice due date without remediation.
- **Material Breach:** Violation of Section 3 (Non-Compete Restrictions).
- **Unauthorized Distribution:** Sharing credentials or redistributing container images.
- **Compliance Audit Failure:** Refusing access to audit or failing inspection.

### 6.2 Upon Termination
- GHCR credentials are **immediately revoked**.
- Existing deployments may continue to run but cannot pull updates.
- New deployments **will fail** to pull enterprise components.
- Support access is **terminated**.
- Licensee must **cease all use** within 30 days.

### 6.3 Remediation Window
For non-malicious breaches, Licensor may offer:
- **60-day cure period** to achieve compliance (e.g., rotate leaked credentials, audit usage).
- **Written warning** with specific remediation steps.
- **Grace period** for accidental credential exposure if caught and rotated immediately.

**Note:** Deliberate non-compete violations (e.g., offering Portage as SaaS) have **zero tolerance** and result in immediate termination.

---

## 7. Data Privacy & Security

### 7.1 Credential Security
- Licensor does **NOT store** raw GitHub PAT tokens.
- GHCR credentials are **isolated to your Kubernetes cluster** via Docker Registry Secrets.
- Pull audit logs are **immutable** and retained for 90 days.
- Licensee is **solely responsible** for credential rotation and secret management.

### 7.2 Usage Telemetry
- Enterprise components send **NO telemetry or analytics** to Licensor.
- All migration activity **remains in your infrastructure**.
- Portage operator logs are **controlled entirely by Licensee**.
- Zero external egress except for critical security updates (configurable).

### 7.3 Compliance Frameworks
Enterprise components are built for:
- **SOC 2 Type II** (supported by Portage infrastructure audits)
- **HIPAA** (encrypted transport, audit logging, access controls)
- **FEDRAMP** (FIPS 140-3 cryptography, red-team tested)
- **PCI DSS** (payment card data migration with zero exposure)

---

## 8. Acceptable Use Policy

### Permitted:
- ✓ Migrating your own production databases across clusters/clouds
- ✓ Internal infrastructure management for your organization
- ✓ Disaster recovery and backup operations
- ✓ Performance testing in non-production environments
- ✓ Security research on your own instances (responsible disclosure required)

### Prohibited:
- ❌ SaaS migrations for external customers
- ❌ Reselling Portage capabilities
- ❌ Competing migration products
- ❌ Rental or timesharing arrangements
- ❌ Removing copyright notices or licensing information
- ❌ Testing or probing Licensor's infrastructure

---

## 9. Warranties & Liability Disclaimers

### 9.1 AS-IS License
Enterprise components are provided **"AS-IS"** without warranty. Licensor disclaims:
- Merchantability or fitness for a particular purpose
- Non-infringement of third-party rights
- Uninterrupted operation or error-free execution

### 9.2 Liability Limitations
Licensor's **total liability** under this license is limited to:
- Refund of pro-rata license fees for the current renewal period
- Nothing more (no consequential, incidental, or punitive damages)

### 9.3 Licensee Responsibility
Licensee assumes **all risk** for:
- Data loss or corruption (mitigated via cryptographic parity verification)
- Downtime or performance issues
- Network outages during migration windows
- Credential compromise due to poor security practices

---

## 10. Intellectual Property & Attribution

### 10.1 Licensor Rights
All enterprise components, algorithms (Merkle tree verification, TCP splicing), and documentation remain the **exclusive property of Licensor**.

### 10.2 Attribution Requirements
Licensee must **retain all copyright notices** and provide attribution:

```text
Portage Platform - Enterprise Edition
Copyright © 2024 Kunal Khare (@kunaldevxxx)
Licensed under Business Source License 1.1 (Non-Compete)
https://github.com/kunaldevxxx/Portage.sh
```

### 10.3 No Trademark Rights
Use of "Portage," "Synapse," "Ghostwire," or Licensor's trademarks requires **explicit written permission**.

---

## 11. Renewal & Upgrade

### 11.1 License Renewal
- Licenses renew **annually** on invoice anniversary date.
- Non-renewal results in **automatic termination** at end of current term.
- 30-day renewal notice is provided via email.

### 11.2 Version Upgrades
- New minor versions (1.0 → 1.1) are included in renewal.
- Major version upgrades (1.x → 2.0) may require **separate licensing agreement**.
- Security patches are **always free**.

### 11.3 Downgrades
- Downgrading to Community Edition (BSL 1.1 free tier) is allowed.
- Enterprise features will be disabled on next reconciliation cycle.
- No pro-rata refunds for early downgrade.

---

## 12. Change of Control

If Licensee is acquired, merged, or undergoes ownership change:

1. **Notify Licensor:** within 30 days of transaction close
2. **Renegotiate Terms:** New entity may require separate license terms
3. **Affiliate Use:** Acquirer's affiliates cannot use license without new agreement
4. **Non-Compete:** Acquirer still bound by Section 3 restrictions

---

## 13. Dispute Resolution & Governing Law

### 13.1 Jurisdiction
This license is governed by **Delaware law** and **federal law** of the United States.

### 13.2 Dispute Resolution
1. **Negotiation:** 30-day good-faith negotiation period
2. **Mediation:** Non-binding mediation in San Francisco, CA (optional)
3. **Arbitration:** Binding arbitration under AAA Commercial Arbitration Rules

### 13.3 Legal Costs
Prevailing party in dispute may recover **reasonable attorneys' fees and costs**.

---

## 14. Amendments & Notices

### 14.1 License Amendments
Licensor may amend this license by **30-day written notice** via email. Licensee's continued use constitutes acceptance.

### 14.2 Notices
All legal notices must be sent to:

**Licensor:**  
Kunal Khare (@kunaldevxxx)  
Email: kunalkhare2004@gmail.com  
GitHub: https://github.com/kunaldevxxx  

**Licensee:**  
Address provided in license agreement

---

## 15. Compliance Checklist

Use this checklist to verify ongoing compliance:

- [ ] GHCR credentials stored in **secure vault** (not hardcoded or in git)
- [ ] Credentials **rotated every 90 days** or upon compromise
- [ ] **No credential sharing** with external organizations
- [ ] Enterprise components deployed **only to internal clusters**
- [ ] **NOT offering migrations** as a service to external customers
- [ ] **Audit logs** accessible for Licensor compliance checks
- [ ] **Copyright notices** retained in all deployments
- [ ] **Support SLA** acknowledged and understood
- [ ] **License agreement** signed by authorized company representative
- [ ] **Escalation contacts** identified for security/license issues

---

## 16. Emergency Contacts

### Security Vulnerability Disclosure
- Email: kunalkhare2004@gmail.com
- Subject: `[SECURITY DISCLOSURE] Portage Enterprise - <Brief Description>`
- Response SLA: 24 hours

### License Compliance Questions
- Email: enterprise@portage.sh
- Subject: `[LICENSE COMPLIANCE] <Your Organization> - <Question>`
- Response SLA: 48 hours

### Account Management
- Email: enterprise@portage.sh
- Contact: Your designated Account Manager
- For: License renewal, upgrades, support tier changes

---

## Appendix A: BSL 1.1 Full Legal Text

See LICENSE file in the root of the repository for complete Business Source License 1.1 legal text.

**Key Points:**
- Free for **internal business use**
- Prohibited for **commercial competition** (SaaS, managed services)
- **Automatic conversion** to Apache 2.0 on October 1, 2028
- **Non-negotiable terms** until Change Date

---

## Appendix B: Credential Security Best Practices

### DO:
```bash
# ✓ Store in HashiCorp Vault
vault kv put secret/portage-ghcr username=xxx token=ghp_xxx

# ✓ Store in AWS Secrets Manager
aws secretsmanager create-secret --name portage-ghcr \
  --secret-string '{"username":"xxx","token":"ghp_xxx"}'

# ✓ Store in 1Password / Bitwarden
# Create entry, share with team via Vault link, rotate quarterly

# ✓ Use in CI/CD via environment variable injection
# GitHub Actions: ${{ secrets.PORTAGE_GHCR_TOKEN }}
```

### DON'T:
```bash
# ✗ Commit to git
git add .env  # DO NOT COMMIT

# ✗ Hardcode in Dockerfiles
ENV GITHUB_TOKEN=ghp_xxx  # SECURITY RISK

# ✗ Log or print credentials
echo "Logging in: $GITHUB_TOKEN"  # LEAKED

# ✗ Share via email or Slack
# "Here's your token: ghp_xxx"  # COMPROMISED

# ✗ Store in plaintext files
cat credentials.txt  # BREACH

# ✗ Use in test commits or PRs
git commit -m "Testing with token ghp_xxx"  # PUBLIC
```

---

**Last Updated:** October 2024  
**Questions?** enterprise@portage.sh  
**License ID Format:** PORTAGE-ENT-YYYY-XXXXX
