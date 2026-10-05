# Portage Enterprise Backend: Customer Onboarding Guide

Welcome to Portage Enterprise! This guide walks you through setting up private access to the enterprise backend components (Synapse-CDC and Ghostwire) for zero-downtime Kubernetes migrations.

---

## Prerequisites

- Active Portage Enterprise License Agreement signed with Portage
- Access to a Kubernetes cluster (v1.22+) with cluster admin permissions
- `kubectl` CLI installed and configured
- `helm` v3+ installed
- GitHub account (for registry authentication)

---

## Step 1: Receive Your GHCR Credentials

Your Portage account manager will provide you with:

```
Enterprise Backend Access Credentials
====================================
GitHub Registry: ghcr.io/kunaldevxxx
Repository: portage-enterprise-backend
Username: <your-github-username>
Access Token: ghp_xxxxxxxxxxxxxxxxxxxx
Token Expiration: <date>
License ID: PORTAGE-ENT-2024-XXXXX
```

**IMPORTANT:** Keep these credentials secure and confidential. Sharing them violates your license agreement.

---

## Step 2: Create Docker Registry Secret in Kubernetes

Replace the placeholder values with your credentials and run:

```bash
# Save your credentials to a variable (for security)
export GITHUB_USERNAME="<your-github-username>"
export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"
export NAMESPACE="portage-system"  # or your preferred namespace

# Create the namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Create the docker registry secret
kubectl create secret docker-registry ghcr-enterprise-credentials \
  --docker-server=ghcr.io \
  --docker-username=$GITHUB_USERNAME \
  --docker-password=$GITHUB_TOKEN \
  --docker-email=<your-email@company.com> \
  --namespace=$NAMESPACE
```

**Or use the automated setup script:**

```bash
# Download and run the setup script
curl -O https://raw.githubusercontent.com/kunaldevxxx/Portage.sh/main/scripts/setup-enterprise-access.sh
chmod +x setup-enterprise-access.sh
./setup-enterprise-access.sh
```

---

## Step 3: Install Portage with Enterprise Backend

Update your `values.yaml` or install with flags:

### Option A: Using values.yaml

```yaml
# values-enterprise.yaml
enterpriseBackend:
  enabled: true
  registry: ghcr.io/kunaldevxxx
  synapseCdc:
    repository: portage-enterprise-synapse-cdc
    tag: "1.0.0"
  ghostwire:
    repository: portage-enterprise-ghostwire
    tag: "1.0.0"
  imagePullSecretName: ghcr-enterprise-credentials

imagePullSecrets:
  - name: ghcr-enterprise-credentials

drivers:
  defaultReplicationDriver: "enterprise-synapse"
  defaultCutoverDriver: "enterprise-ghostwire"
  enterpriseSynapseEndpoint: "http://synapse-cdc.portage-system.svc:9000"
  enterpriseGhostwireEndpoint: "http://ghostwire.portage-system.svc:8000"
```

**Install:**
```bash
helm install portage-operator ./charts/portage-operator \
  --namespace portage-system \
  --create-namespace \
  -f values-enterprise.yaml
```

### Option B: Helm CLI flags (quick install)

```bash
helm install portage-operator ./charts/portage-operator \
  --namespace portage-system \
  --create-namespace \
  --set enterpriseBackend.enabled=true \
  --set imagePullSecrets[0].name=ghcr-enterprise-credentials \
  --set drivers.defaultReplicationDriver=enterprise-synapse \
  --set drivers.defaultCutoverDriver=enterprise-ghostwire \
  --set drivers.enterpriseSynapseEndpoint="http://synapse-cdc.portage-system.svc:9000" \
  --set drivers.enterpriseGhostwireEndpoint="http://ghostwire.portage-system.svc:8000"
```

---

## Step 4: Verify Installation

Check that the enterprise backend components are running:

```bash
# Check pod status
kubectl get pods -n portage-system

# Verify Synapse-CDC is accessible
kubectl port-forward -n portage-system svc/synapse-cdc 9000:9000
curl -s http://localhost:9000/health

# Verify Ghostwire is accessible
kubectl port-forward -n portage-system svc/ghostwire 8000:8000
curl -s http://localhost:8000/health
```

Expected output:
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "component": "enterprise-synapse-cdc"
}
```

---

## Step 5: Run Your First Migration

Create a PortageMigration resource with enterprise drivers:

```yaml
apiVersion: migration.portage.sh/v1alpha1
kind: PortageMigration
metadata:
  name: postgres-production-migration
  namespace: portage-system
spec:
  sourceWorkload:
    kind: StatefulSet
    name: postgres-primary
  sourceNamespace: database-prod
  targetNamespace: database-modern-prod

  # Use enterprise-grade drivers for zero-downtime
  cdcReplication:
    driver: enterprise-synapse
    enabled: true
    endpoint: "http://synapse-cdc.portage-system.svc:9000"
    streamID: "pg-wal-stream-prod"
    requireMerkleVerification: true
    maxAllowedLagSeconds: 2

  trafficCutover:
    driver: enterprise-ghostwire
    enabled: true
    directorEndpoint: "http://ghostwire.portage-system.svc:8000"
    routeID: "postgres-traffic-route"
    targetEndpoint: "postgres-svc.database-modern-prod.svc:5432"
    drainTimeoutSeconds: 15

  volumeMigration:
    volumeSnapshotClassName: csi-aws-vsc
    targetStorageClassName: gp3-encrypted
    persistentVolumeClaimNames:
      - data-postgres-primary-0
    retainOnSource: true
```

**Apply the migration:**
```bash
kubectl apply -f migration.yaml

# Watch progress
kubectl get portagemigration -w
```

---

## Troubleshooting

### Pull Error: "unauthorized: authentication required"

**Cause:** GHCR credentials are invalid or expired.

**Fix:**
```bash
# Verify credentials
echo $GITHUB_TOKEN | docker login ghcr.io -u $GITHUB_USERNAME --password-stdin

# If login fails, contact your Portage account manager for a new token

# Delete and recreate the secret
kubectl delete secret ghcr-enterprise-credentials -n portage-system
kubectl create secret docker-registry ghcr-enterprise-credentials \
  --docker-server=ghcr.io \
  --docker-username=$GITHUB_USERNAME \
  --docker-password=<NEW_TOKEN> \
  --namespace=portage-system
```

### Synapse-CDC unhealthy

**Check logs:**
```bash
kubectl logs -n portage-system -l app=synapse-cdc --tail=100
```

**Verify endpoint:**
```bash
kubectl exec -it -n portage-system deployment/portage-operator -- \
  curl -s http://synapse-cdc:9000/health | jq .
```

### Ghostwire not routing traffic

**Check proxy logs:**
```bash
kubectl logs -n portage-system -l app=ghostwire --tail=100
```

**Verify routes:**
```bash
kubectl exec -it -n portage-system deployment/ghostwire -- \
  curl -s http://localhost:8000/routes | jq .
```

---

## Security Best Practices

1. **Rotate Tokens Regularly:** Request new GHCR tokens from your account manager every 90 days.
2. **Monitor Access:** Review GHCR audit logs (provided by Portage) for unauthorized pull attempts.
3. **Audit Logs:** Enable Kubernetes audit logging to track who deployed migration resources.
4. **Network Policies:** Restrict egress from your cluster to only Portage-required endpoints.
5. **Secret Rotation:** Automate secret rotation using a tool like Sealed Secrets or External Secrets Operator.

---

## License Compliance

**Your Portage Enterprise License includes:**
- Private access to enterprise-grade components
- Support SLA: 24-hour response, 7-day patch SLA
- 90-day token expiration + auto-renewal
- Audit trail of all registry pulls

**Prohibited:**
- Sharing GHCR credentials with other organizations
- Redistributing container images outside your organization
- Using enterprise components for SaaS offerings
- Reverse-engineering or modifying components

**Violations:** Material breach of license agreement. Contact your Portage account manager immediately if credentials are compromised.

---

## Support & Escalation

### Regular Support
- Email: enterprise-support@portage.sh
- GitHub: [Private Security Advisories](https://github.com/kunaldevxxx/Portage.sh/security)
- Slack: #portage-enterprise (invite-only)

### Emergency Support
- Phone: +1-XXX-XXX-XXXX (enterprise customers only)
- Email: emergency@portage.sh (mark subject as `[URGENT]`)
- Response SLA: 1 hour

### Account Management
- Account Manager: [Your assigned contact]
- Email: enterprise@portage.sh
- Licensing & Renewals: billing@portage.sh

---

## Appendix: Environment Variables

Store these securely (e.g., in 1Password, AWS Secrets Manager):

```bash
PORTAGE_GHCR_SERVER="ghcr.io"
PORTAGE_GHCR_USERNAME="<github-username>"
PORTAGE_GHCR_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"
PORTAGE_GHCR_EMAIL="<your-email@company.com>"
PORTAGE_K8S_NAMESPACE="portage-system"
PORTAGE_LICENSE_ID="PORTAGE-ENT-2024-XXXXX"
PORTAGE_SYNAPSE_ENDPOINT="http://synapse-cdc.portage-system.svc:9000"
PORTAGE_GHOSTWIRE_ENDPOINT="http://ghostwire.portage-system.svc:8000"
```

---

**Last Updated:** October 2024  
**Version:** 1.0.0  
**Contact:** kunal.khare@improving.com
