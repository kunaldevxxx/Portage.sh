#!/bin/bash
set -euo pipefail

# Portage Enterprise: Automated GHCR Access Setup
# This script creates a Kubernetes docker registry secret for private enterprise backend access

SCRIPT_VERSION="1.0.0"
GHCR_SERVER="ghcr.io"
GHCR_REGISTRY_PATH="kunaldevxxx"
SECRET_NAME="ghcr-enterprise-credentials"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}ℹ${NC} $*"
}

log_success() {
    echo -e "${GREEN}✓${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}⚠${NC} $*"
}

log_error() {
    echo -e "${RED}✗${NC} $*"
}

# Print header
print_header() {
    cat <<EOF
╔════════════════════════════════════════════════════════════════╗
║         Portage Enterprise: GHCR Access Setup v${SCRIPT_VERSION}         ║
║                                                                ║
║    Configure private Kubernetes access to enterprise backend    ║
│    components (Synapse-CDC, Ghostwire)                         ║
╚════════════════════════════════════════════════════════════════╝
EOF
    echo
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."

    local missing=()

    if ! command -v kubectl &> /dev/null; then
        missing+=("kubectl")
    fi

    if ! command -v docker &> /dev/null; then
        missing+=("docker")
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing[*]}"
        log_info "Please install: https://kubernetes.io/docs/tasks/tools/"
        exit 1
    fi

    log_success "All prerequisites met (kubectl, docker)"
}

# Get GHCR credentials from user
get_credentials() {
    log_info "Enter your GHCR access credentials (provided by Portage)"
    echo

    # GitHub username
    read -p "GitHub Username: " -r GITHUB_USERNAME
    if [[ -z "$GITHUB_USERNAME" ]]; then
        log_error "GitHub username cannot be empty"
        exit 1
    fi

    # GitHub personal access token (sensitive input)
    read -s -p "GitHub Personal Access Token (ghp_...): " -r GITHUB_TOKEN
    echo
    if [[ -z "$GITHUB_TOKEN" ]]; then
        log_error "GitHub token cannot be empty"
        exit 1
    fi

    # Email (optional)
    read -p "Email address [$(git config user.email 2>/dev/null || echo 'user@company.com')]: " -r EMAIL
    EMAIL="${EMAIL:-$(git config user.email 2>/dev/null || echo 'user@company.com')}"
}

# Validate credentials against GHCR
validate_credentials() {
    log_info "Validating GHCR credentials..."

    if echo "$GITHUB_TOKEN" | docker login "$GHCR_SERVER" -u "$GITHUB_USERNAME" --password-stdin &>/dev/null; then
        log_success "GHCR authentication successful"
        return 0
    else
        log_error "GHCR authentication failed. Check your credentials."
        return 1
    fi
}

# Get Kubernetes context
get_k8s_context() {
    log_info "Kubernetes Configuration"

    CURRENT_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "")
    CURRENT_NS=$(kubectl config view --minify --output 'jsonpath={.contexts[0].context.namespace}' 2>/dev/null || echo "default")

    log_info "Current context: ${CURRENT_CONTEXT:-<none>}"
    log_info "Default namespace: ${CURRENT_NS:-default}"

    read -p "Target namespace [portage-system]: " -r NAMESPACE
    NAMESPACE="${NAMESPACE:-portage-system}"

    log_info "Creating namespace '$NAMESPACE' if it doesn't exist..."
    kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    log_success "Namespace ready: $NAMESPACE"
}

# Create docker registry secret
create_secret() {
    log_info "Creating docker registry secret in cluster..."

    # Check if secret already exists
    if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" &>/dev/null; then
        log_warn "Secret '$SECRET_NAME' already exists in namespace '$NAMESPACE'"
        read -p "Overwrite existing secret? (y/N): " -r OVERWRITE
        if [[ "$OVERWRITE" != "y" && "$OVERWRITE" != "Y" ]]; then
            log_info "Skipping secret creation"
            return 0
        fi
        kubectl delete secret "$SECRET_NAME" -n "$NAMESPACE" >/dev/null
    fi

    # Create the secret
    kubectl create secret docker-registry "$SECRET_NAME" \
        --docker-server="$GHCR_SERVER" \
        --docker-username="$GITHUB_USERNAME" \
        --docker-password="$GITHUB_TOKEN" \
        --docker-email="$EMAIL" \
        --namespace="$NAMESPACE" >/dev/null

    log_success "Secret created: $SECRET_NAME in namespace $NAMESPACE"
}

# Verify secret
verify_secret() {
    log_info "Verifying secret configuration..."

    SECRET_DATA=$(kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d | grep -o "ghcr.io" || echo "")

    if [[ -n "$SECRET_DATA" ]]; then
        log_success "Secret verified successfully"
        return 0
    else
        log_error "Failed to verify secret"
        return 1
    fi
}

# Test image pull
test_image_pull() {
    log_info "Testing image pull from private GHCR..."

    # Create a test pod that tries to pull the image
    TEST_POD_NAME="portage-ghcr-test-$(date +%s)"
    TEST_IMAGE="${GHCR_SERVER}/${GHCR_REGISTRY_PATH}/portage-enterprise-synapse-cdc:1.0.0"

    cat <<EOF | kubectl apply -n "$NAMESPACE" -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: $TEST_POD_NAME
spec:
  imagePullSecrets:
    - name: $SECRET_NAME
  containers:
    - name: test
      image: $TEST_IMAGE
      imagePullPolicy: Always
      command: ["sleep", "10"]
  restartPolicy: Never
  terminationGracePeriodSeconds: 5
EOF

    log_info "Waiting for pod to pull image (20 seconds)..."

    # Wait for pod to pull image
    local elapsed=0
    local timeout=20

    while [[ $elapsed -lt $timeout ]]; do
        STATUS=$(kubectl get pod "$TEST_POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")

        if [[ "$STATUS" == "Running" || "$STATUS" == "Succeeded" ]]; then
            log_success "Image pull successful!"
            kubectl delete pod "$TEST_POD_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1
            return 0
        elif [[ "$STATUS" == "Failed" ]]; then
            ERROR=$(kubectl describe pod "$TEST_POD_NAME" -n "$NAMESPACE" | grep -A 5 "Error" | head -3 || echo "Unknown error")
            log_error "Image pull failed: $ERROR"
            kubectl delete pod "$TEST_POD_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1
            return 1
        fi

        sleep 2
        ((elapsed += 2))
    done

    log_warn "Image pull test timed out. Pod may still be pulling."
    kubectl delete pod "$TEST_POD_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1
    return 0
}

# Print summary
print_summary() {
    cat <<EOF

╔════════════════════════════════════════════════════════════════╗
║                    Setup Complete ✓                            ║
╚════════════════════════════════════════════════════════════════╝

Enterprise backend access is ready!

Secret Details:
  - Name: $SECRET_NAME
  - Namespace: $NAMESPACE
  - Registry: $GHCR_SERVER
  - User: $GITHUB_USERNAME

Next Steps:

1. Install Portage Helm chart with enterprise backend:

   helm install portage-operator ./charts/portage-operator \\
     --namespace $NAMESPACE \\
     --set enterpriseBackend.enabled=true \\
     --set imagePullSecrets[0].name=$SECRET_NAME \\
     --set drivers.defaultReplicationDriver=enterprise-synapse \\
     --set drivers.defaultCutoverDriver=enterprise-ghostwire

2. Verify installation:

   kubectl get pods -n $NAMESPACE
   kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=portage-operator

3. Create a PortageMigration resource:

   kubectl apply -f migration.yaml

📚 Documentation: docs/ENTERPRISE_ONBOARDING.md
🔗 License Agreement: LICENSE
📧 Support: enterprise-support@portage.sh

⚠️  Keep your credentials secure:
   - Do not commit PAT to git repositories
   - Rotate tokens every 90 days
   - Monitor GHCR audit logs for unauthorized access

EOF
}

# Main execution
main() {
    print_header
    check_prerequisites
    echo
    get_credentials
    echo

    if ! validate_credentials; then
        log_error "Cannot proceed without valid GHCR credentials"
        exit 1
    fi

    echo
    get_k8s_context
    echo
    create_secret
    echo
    verify_secret
    echo
    test_image_pull
    echo
    print_summary
}

# Error handler
trap 'log_error "Script failed"; exit 1' ERR

# Execute
main "$@"
