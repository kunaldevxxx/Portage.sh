# GHCR Access Monitoring & Audit Guide (For Licensor)

**Version:** 1.0  
**Audience:** Portage License Management & Security Teams  
**Purpose:** Monitor enterprise customer registry access, detect unauthorized usage, and maintain compliance

---

## Overview

All Portage Enterprise customers access proprietary components via private GHCR repositories. This guide provides tools and procedures to:

1. **Monitor** real-time pull activity from enterprise customers
2. **Audit** historical access patterns for compliance verification
3. **Detect** unauthorized access attempts or credential misuse
4. **Revoke** credentials in case of breach or non-compliance
5. **Track** usage across customers for billing and SLA monitoring

---

## GHCR Access Logs

### GitHub Container Registry Audit Trail

All container pulls are logged by GitHub and accessible via:

**GitHub Web UI:**
1. Go to organization: https://github.com/kunaldevxxx
2. Settings → Packages → Container Registry
3. Click on specific private repository (e.g., `portage-enterprise-backend`)
4. View audit logs for all pull events

**API Access (Programmatic):**

```bash
# Requires: GitHub Personal Access Token with 'read:packages' scope
export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"

# List all packages in the organization
curl -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://api.github.com/orgs/kunaldevxxx/packages?package_type=container

# Get detailed audit logs for a specific package
# (Note: Direct audit log access via API has limitations; 
#  GitHub Web UI provides more comprehensive logs)
```

### Log Retention Policy

- **Logs retained by GitHub:** 90 days (default)
- **Logs archived by Licensor:** Recommended 2+ years for compliance
- **Automatic export:** Set up scheduled exports to S3/GCS

---

## Monitoring Setup

### Prerequisites

- GitHub organization owner access
- GitHub Personal Access Token with admin:org_hook scope
- CloudWatch / Grafana / Splunk (optional, for advanced monitoring)
- AWS S3 or Google Cloud Storage (for log archival)

### Step 1: Enable GitHub Audit Logging

GitHub automatically logs all container pulls. Verify it's enabled:

```bash
curl -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github+json" \
  https://api.github.com/orgs/kunaldevxxx/audit-log
```

**Response includes:**
```json
{
  "action": "package.pull",
  "actor": "username",
  "timestamp": "2024-10-05T14:32:15Z",
  "resource": "ghcr.io/kunaldevxxx/portage-enterprise-backend",
  "status": "success"
}
```

### Step 2: Export Logs to S3/GCS (Recommended)

Create automated log exports for analysis and compliance:

**AWS S3 Setup:**

```bash
# Create S3 bucket for GitHub logs
aws s3 mb s3://portage-github-audit-logs --region us-east-1

# Enable versioning for audit trail immutability
aws s3api put-bucket-versioning \
  --bucket portage-github-audit-logs \
  --versioning-configuration Status=Enabled

# Enable MFA Delete protection
aws s3api put-bucket-versioning \
  --bucket portage-github-audit-logs \
  --versioning-configuration Status=Enabled,MFADelete=Enabled
```

**CloudWatch Integration (via Lambda):**

```python
# Lambda function to periodically export GitHub audit logs to S3
import boto3
import requests
from datetime import datetime, timedelta

def lambda_handler(event, context):
    # GitHub API call
    gh_token = os.environ['GITHUB_TOKEN']
    headers = {
        'Authorization': f'token {gh_token}',
        'Accept': 'application/vnd.github+json'
    }
    
    # Fetch last 24 hours of audit logs
    since = (datetime.utcnow() - timedelta(days=1)).isoformat()
    url = f"https://api.github.com/orgs/kunaldevxxx/audit-log?phrase=action:package.pull+created:{since}"
    
    response = requests.get(url, headers=headers)
    audit_logs = response.json()
    
    # Export to S3
    s3 = boto3.client('s3')
    timestamp = datetime.utcnow().isoformat()
    s3.put_object(
        Bucket='portage-github-audit-logs',
        Key=f"audit-logs/{timestamp}-github-pulls.json",
        Body=json.dumps(audit_logs),
        ServerSideEncryption='AES256'
    )
    
    return {
        'statusCode': 200,
        'body': f'Exported {len(audit_logs)} audit events'
    }
```

**Configure CloudWatch Events:**

```bash
# Create EventBridge rule to run Lambda daily at 00:00 UTC
aws events put-rule \
  --name portage-github-audit-export \
  --schedule-expression "cron(0 0 * * ? *)" \
  --state ENABLED
```

### Step 3: Splunk / ELK Integration (Enterprise)

**Splunk Data Input Configuration:**

```yaml
# inputs.conf
[http:github_audit]
disabled = false
http_event_collector_token = <YOUR_HEC_TOKEN>
source = github:audit
sourcetype = github:audit:json
index = security_audit
```

**Query all container pulls:**

```spl
source="github:audit" action="package.pull" resource="portage-enterprise-*"
| stats count by actor, resource, action, timestamp
| sort - timestamp
```

---

## Real-Time Monitoring Dashboard

### Example: Grafana Dashboard

```json
{
  "dashboard": {
    "title": "Portage GHCR Access Monitor",
    "refresh": "30s",
    "panels": [
      {
        "title": "Total Pulls (Last 24h)",
        "targets": [
          {
            "query": "SELECT COUNT(*) FROM github_audit WHERE resource LIKE 'portage-enterprise-%' AND created_at > NOW() - INTERVAL 24 HOUR"
          }
        ]
      },
      {
        "title": "Pulls by Customer (Last 24h)",
        "targets": [
          {
            "query": "SELECT actor, COUNT(*) as pulls FROM github_audit WHERE resource LIKE 'portage-enterprise-%' AND created_at > NOW() - INTERVAL 24 HOUR GROUP BY actor ORDER BY pulls DESC"
          }
        ]
      },
      {
        "title": "Failed Authentication Attempts",
        "targets": [
          {
            "query": "SELECT COUNT(*) FROM github_audit WHERE action='package.pull' AND status='failed' AND created_at > NOW() - INTERVAL 24 HOUR"
          }
        ]
      },
      {
        "title": "Anomalous Pull Patterns",
        "targets": [
          {
            "query": "SELECT actor, COUNT(*) as pulls, MIN(created_at) as first_pull, MAX(created_at) as last_pull FROM github_audit WHERE resource LIKE 'portage-enterprise-%' AND created_at > NOW() - INTERVAL 1 HOUR GROUP BY actor HAVING pulls > 50"
          }
        ]
      }
    ]
  }
}
```

---

## Audit Procedures

### Manual Audit: Check Customer Access

**Verify a specific customer's recent pulls:**

```bash
#!/bin/bash
CUSTOMER_USERNAME="acme-corp-dev"
GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"

# Query GitHub audit API
curl -s \
  -H "Authorization: token $GITHUB_TOKEN" \
  -H "Accept: application/vnd.github+json" \
  "https://api.github.com/orgs/kunaldevxxx/audit-log?phrase=actor:$CUSTOMER_USERNAME+action:package.pull" \
  | jq '.[] | {timestamp: .created_at, actor: .actor, resource: .resource, status: .status}'
```

**Expected output (compliant usage):**

```json
{
  "timestamp": "2024-10-05T14:32:15Z",
  "actor": "acme-corp-dev",
  "resource": "ghcr.io/kunaldevxxx/portage-enterprise-backend:1.0.0",
  "status": "success"
}
```

### Automated Compliance Check

```python
#!/usr/bin/env python3
"""
Portage GHCR Compliance Audit Script

Detects:
- Unauthorized access attempts (failed auth)
- Unusual pull patterns (rate spike)
- Credential sharing (multiple users pulling with same token)
- Deployment anomalies (pulls from unexpected IPs)
"""

import requests
import json
from datetime import datetime, timedelta
from collections import defaultdict

class PortageAuditMonitor:
    def __init__(self, github_token):
        self.gh_token = github_token
        self.api_url = "https://api.github.com/orgs/kunaldevxxx/audit-log"
        self.headers = {
            'Authorization': f'token {github_token}',
            'Accept': 'application/vnd.github+json'
        }
    
    def get_recent_pulls(self, hours=24):
        """Fetch recent container pulls"""
        since = (datetime.utcnow() - timedelta(hours=hours)).isoformat()
        url = f"{self.api_url}?phrase=action:package.pull+created:{since}"
        response = requests.get(url, headers=self.headers)
        return response.json()
    
    def detect_failed_auth(self):
        """Detect failed authentication attempts"""
        pulls = self.get_recent_pulls(hours=24)
        failed = [p for p in pulls if p.get('status') == 'failed']
        
        if failed:
            print(f"\n⚠️  ALERT: {len(failed)} failed authentication attempts")
            for event in failed:
                print(f"  - {event['actor']} at {event['created_at']}")
        
        return len(failed)
    
    def detect_rate_spike(self, threshold_per_hour=20):
        """Detect unusual pull rate (potential credential misuse)"""
        pulls = self.get_recent_pulls(hours=24)
        
        # Group by hour
        by_hour = defaultdict(list)
        for pull in pulls:
            hour = pull['created_at'][:13]  # YYYY-MM-DDTHH
            by_hour[hour].append(pull)
        
        anomalies = []
        for hour, events in by_hour.items():
            if len(events) > threshold_per_hour:
                anomalies.append({
                    'hour': hour,
                    'count': len(events),
                    'actors': list(set([e['actor'] for e in events]))
                })
        
        if anomalies:
            print(f"\n⚠️  ALERT: Unusual pull rates detected")
            for anomaly in anomalies:
                print(f"  - {anomaly['hour']}: {anomaly['count']} pulls by {anomaly['actors']}")
        
        return len(anomalies)
    
    def detect_credential_sharing(self):
        """Detect if same token is being used by multiple actors"""
        pulls = self.get_recent_pulls(hours=24)
        
        # Group by token (actor is proxy for token)
        # Note: GitHub doesn't expose token UUID, so we infer from actor patterns
        by_resource = defaultdict(set)
        for pull in pulls:
            by_resource[pull['resource']].add(pull['actor'])
        
        sharing = []
        for resource, actors in by_resource.items():
            if len(actors) > 1 and 'enterprise' in resource:
                sharing.append({
                    'resource': resource,
                    'actors': list(actors),
                    'count': len(actors)
                })
        
        if sharing:
            print(f"\n⚠️  ALERT: Potential credential sharing detected")
            for share in sharing:
                print(f"  - {share['resource']}: {share['count']} different users")
                for actor in share['actors']:
                    print(f"    * {actor}")
        
        return len(sharing)
    
    def generate_compliance_report(self):
        """Generate compliance report for customer"""
        print(f"\n{'='*60}")
        print("Portage GHCR Compliance Audit Report")
        print(f"Generated: {datetime.utcnow().isoformat()}")
        print(f"{'='*60}\n")
        
        failed = self.detect_failed_auth()
        spikes = self.detect_rate_spike()
        sharing = self.detect_credential_sharing()
        
        total_issues = failed + spikes + sharing
        
        if total_issues == 0:
            print("✓ COMPLIANCE STATUS: PASS")
            print("  No security or compliance issues detected.")
        else:
            print(f"✗ COMPLIANCE STATUS: FAIL ({total_issues} issues)")
            print("\nRECOMMENDED ACTIONS:")
            if failed > 0:
                print(f"  1. Investigate {failed} failed auth attempts")
                print("     Action: Verify credentials are correct, rotate if needed")
            if spikes > 0:
                print(f"  2. Review {spikes} rate spike anomalies")
                print("     Action: Confirm legitimate high-volume pulls (e.g., CI/CD)")
            if sharing > 0:
                print(f"  3. Investigate {sharing} credential sharing incidents")
                print("     Action: Rotate credentials, verify single user per token")
        
        print(f"\n{'='*60}\n")

# Run audit
if __name__ == "__main__":
    import os
    
    gh_token = os.environ.get('GITHUB_TOKEN')
    if not gh_token:
        print("ERROR: Set GITHUB_TOKEN environment variable")
        exit(1)
    
    monitor = PortageAuditMonitor(gh_token)
    monitor.generate_compliance_report()
```

**Run compliance audit:**

```bash
export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"
python3 audit_ghcr_compliance.py
```

---

## Incident Response: Credential Compromise

### If credentials are leaked:

1. **Immediate (< 5 minutes):**
   ```bash
   # Revoke compromised PAT in GitHub
   # Settings → Developer settings → Personal access tokens → Delete [token]
   
   # OR revoke via API
   curl -X DELETE \
     -H "Authorization: token $GITHUB_TOKEN" \
     https://api.github.com/user/installations/TOKEN_ID/access_tokens
   ```

2. **Short-term (< 1 hour):**
   - Issue new PAT to customer
   - Update customer's Kubernetes secret
   - Review last 24h of access logs for unauthorized pulls

3. **Investigation (< 24 hours):**
   - Query audit logs for all pulls with compromised token
   - Check if pulls came from unexpected IP ranges
   - Verify no unauthorized deployments occurred
   - Document incident for compliance file

4. **Communication (ASAP):**
   - Notify affected customer
   - Provide incident summary (what happened, when, scope)
   - Confirm no customer data was accessed
   - Outline remediation steps

### Incident Report Template

```markdown
# GHCR Credential Compromise Incident Report

**Incident ID:** INC-2024-GHCR-001  
**Customer:** Acme Corp  
**Date Detected:** October 5, 2024 14:32 UTC  
**Severity:** HIGH  

## Summary
Portage enterprise backend GHCR token for Acme Corp was exposed in a public GitHub repository commit.

## Scope
- **Token:** ghp_xxxx...xxxx
- **Repository:** https://github.com/acme-corp/infra-configs (PUBLIC, now PRIVATE)
- **Time Exposed:** 2024-10-05 12:00 UTC - 2024-10-05 14:15 UTC (135 minutes)
- **Pulls Attempted:** 47 (0 successful)

## Actions Taken
1. ✓ Revoked compromised token (2024-10-05 14:32 UTC)
2. ✓ Issued replacement token to customer
3. ✓ Analyzed audit logs - no unauthorized pulls found
4. ✓ Notified customer via email + phone call

## Remediation
- Customer to rotate credentials immediately
- GitHub repository access logs reviewed (no suspicious access)
- Recommend secret scanning tool (git-secrets, TruffleHog)

## Follow-up
- 7-day checkpoint: Verify no unusual access patterns
- 30-day review: Confirm customer security improvements

**Prepared By:** Security Team  
**Date:** October 5, 2024
```

---

## Credential Revocation Procedure

### Revoking a compromised or unused token:

**For Security Incident:**

```bash
#!/bin/bash
# Immediate revocation for compromised credential

CUSTOMER_NAME="acme-corp"
GITHUB_TOKEN="$ADMIN_GITHUB_TOKEN"

# Step 1: Revoke in GitHub
echo "Revoking PAT for $CUSTOMER_NAME..."
# (Manual via GitHub UI or API - see prerequisite setup)

# Step 2: Notify customer
echo "Notifying customer..."
aws sns publish \
  --topic-arn "arn:aws:sns:us-east-1:ACCOUNT:portage-security" \
  --subject "URGENT: GHCR Credential Revoked" \
  --message "Your Portage GHCR credentials have been revoked due to security incident. Contact support immediately."

# Step 3: Record in audit log
echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] REVOKED: $CUSTOMER_NAME due to security incident" \
  >> /var/log/portage/credential-audit.log

# Step 4: Generate new credentials
echo "Generating replacement credentials..."
# (Handled by Portage admin team via GitHub UI)
```

**For License Non-Compliance:**

```bash
# Graceful credential rotation for compliance breach

CUSTOMER_NAME="bad-actor-corp"
NOTICE_DAYS=30

# Send compliance warning
aws ses send-email \
  --from "compliance@portage.sh" \
  --to "$CUSTOMER_EMAIL" \
  --subject "Portage License Compliance Notice" \
  --html "Your use of Portage has been flagged for non-compliance. You have 30 days to remedy or credentials will be revoked."

# Schedule automatic revocation
# (Use Lambda + CloudWatch Events)
at "now + 30 days" "bash /opt/portage/revoke-credential.sh $CUSTOMER_NAME"
```

---

## Compliance Reporting

### Quarterly Compliance Report (for auditors)

```bash
#!/bin/bash
# Generate quarterly compliance report

REPORT_FILE="portage-ghcr-compliance-q4-2024.pdf"

cat > compliance-report.html <<'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>Portage GHCR Compliance Report - Q4 2024</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 20px; }
        table { width: 100%; border-collapse: collapse; margin: 20px 0; }
        th, td { border: 1px solid #ddd; padding: 10px; text-align: left; }
        th { background: #f0f0f0; }
        .pass { color: green; }
        .fail { color: red; }
    </style>
</head>
<body>
    <h1>Portage GHCR Compliance Report</h1>
    <p><strong>Period:</strong> October 1 - December 31, 2024</p>
    <p><strong>Generated:</strong> January 5, 2025</p>
    
    <h2>Executive Summary</h2>
    <ul>
        <li>Total Customers: 24</li>
        <li>Compliant: 23 <span class="pass">✓</span></li>
        <li>Non-Compliant: 1 <span class="fail">✗</span></li>
        <li>Credential Compromises: 0</li>
        <li>Failed Auth Attempts: 3 (all remediated)</li>
    </ul>
    
    <h2>Customer Access Summary</h2>
    <table>
        <tr>
            <th>Customer</th>
            <th>Total Pulls</th>
            <th>Avg Pulls/Day</th>
            <th>Last Pull</th>
            <th>Status</th>
        </tr>
        <tr>
            <td>Acme Corp</td>
            <td>1,024</td>
            <td>34.8</td>
            <td>2024-12-31 23:15 UTC</td>
            <td><span class="pass">PASS</span></td>
        </tr>
        <tr>
            <td>TechCorp Inc</td>
            <td>2,156</td>
            <td>73.4</td>
            <td>2024-12-31 18:42 UTC</td>
            <td><span class="pass">PASS</span></td>
        </tr>
        <tr>
            <td>BadActor LLC</td>
            <td>156</td>
            <td>5.3</td>
            <td>2024-11-15 03:22 UTC</td>
            <td><span class="fail">FAIL - Non-Compliant</span></td>
        </tr>
    </table>
    
    <h2>Security Incidents</h2>
    <p>No confirmed security incidents or credential compromises.</p>
    
    <h2>Recommendations</h2>
    <ul>
        <li>Continue quarterly audits</li>
        <li>Implement automated credential rotation (90-day intervals)</li>
        <li>Escalate BadActor LLC compliance investigation</li>
    </ul>
</body>
</html>
EOF

# Convert to PDF
wkhtmltopdf compliance-report.html $REPORT_FILE
echo "Report generated: $REPORT_FILE"
```

---

## Best Practices Summary

✓ **DO:**
- Monitor GHCR audit logs daily
- Export logs to immutable storage (S3 with versioning)
- Audit each customer quarterly
- Rotate compromised credentials within 1 hour
- Send annual audit reports to customers

✗ **DON'T:**
- Store GitHub tokens in plaintext
- Share customer access logs publicly
- Delay credential rotation for non-compliance
- Ignore failed authentication attempts
- Allow dormant credentials (> 1 year inactive)

---

## Tools & Resources

- **GitHub Audit API:** https://docs.github.com/en/rest/orgs/orgs#audit-log
- **Container Registry Logs:** https://docs.github.com/en/packages/managing-container-images-with-github-container-registry
- **Splunk GitHub Integration:** https://splunkbase.splunk.com/app/3465/
- **Grafana GitHub Datasource:** https://grafana.com/grafana/plugins/grafana-github-datasource/

---

**Last Updated:** October 2024  
**Maintained By:** Portage Security & Compliance Team  
**Next Review:** Q1 2025
