#!/usr/bin/env bash
# ==============================================================================
# Portage: Spin Up a Real Local Kubernetes Cluster (Kind) with Live PVC & Database
# ==============================================================================
set -euo pipefail

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

echo -e "${BOLD}${CYAN}"
echo "================================================================================"
echo "    SETTING UP REAL LOCAL KUBERNETES CLUSTER FOR LIVE DATA VALIDATION"
echo "================================================================================"
echo -e "${NC}"

# Check Docker
if ! docker info >/dev/null 2>&1; then
  echo -e "${YELLOW}Notice: Docker daemon is not running on your Mac.${NC}"
  echo "Please start Docker Desktop, then rerun this script."
  exit 1
fi

# Check Kind
if ! command -v kind >/dev/null 2>&1; then
  echo -e "${YELLOW}Notice: 'kind' is not installed. Installing via Homebrew or use minikube:${NC}"
  echo "brew install kind"
  exit 1
fi

CLUSTER_NAME="portage-demo"
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo -e "${BOLD}[1/4] Creating local Kubernetes cluster '${CLUSTER_NAME}' via Kind...${NC}"
  kind create cluster --name "${CLUSTER_NAME}"
else
  echo -e "${GREEN}✔ Local cluster '${CLUSTER_NAME}' is already running.${NC}"
  kubectl config use-context "kind-${CLUSTER_NAME}"
fi

echo -e "${BOLD}[2/4] Deploying real PostgreSQL workload with local PersistentVolumeClaim...${NC}"
cat << 'YAML' | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: database-prod
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data-postgres-0
  namespace: database-prod
spec:
  accessModes: [ "ReadWriteOnce" ]
  resources:
    requests:
      storage: 1Gi
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres-primary
  namespace: database-prod
spec:
  serviceName: postgres-svc
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
        - name: postgres
          image: postgres:15-alpine
          env:
            - name: POSTGRES_PASSWORD
              value: "postgres"
            - name: POSTGRES_DB
              value: "core_banking"
          ports:
            - containerPort: 5432
          volumeMounts:
            - mountPath: /var/lib/postgresql/data
              name: pgdata
      volumes:
        - name: pgdata
          persistentVolumeClaim:
            claimName: data-postgres-0
YAML

echo -e "${BOLD}[3/4] Awaiting PostgreSQL pod to become Ready...${NC}"
kubectl wait --namespace database-prod \
  --for=condition=ready pod \
  --selector=app=postgres \
  --timeout=120s

echo -e "${BOLD}[4/4] Seeding live database with sample financial transaction records...${NC}"
kubectl exec -i -n database-prod postgres-primary-0 -- psql -U postgres -d core_banking << 'SQL'
CREATE TABLE IF NOT EXISTS accounts (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100),
    balance NUMERIC(15,2)
);
CREATE TABLE IF NOT EXISTS transactions (
    id SERIAL PRIMARY KEY,
    account_id INT,
    amount NUMERIC(15,2),
    status VARCHAR(20),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO accounts (name, balance) VALUES 
('Enterprise Treasury Alpha', 10000000.00),
('Global Settlement Pool', 5000000.00);
INSERT INTO transactions (account_id, amount, status) 
SELECT 1, (random() * 1000)::numeric(15,2), 'COMPLETED'
FROM generate_series(1, 1000);
SQL

echo ""
echo -e "${BOLD}${GREEN}================================================================================${NC}"
echo -e "${BOLD}${GREEN}    LIVE KUBERNETES WORKLOAD READY! YOU CAN NOW RUN MANUAL COMMANDS:${NC}"
echo -e "${BOLD}${GREEN}================================================================================${NC}"
echo ""
echo -e "1. Check the real PersistentVolumeClaim:"
echo -e "   ${CYAN}kubectl get pvc -n database-prod${NC}"
echo ""
echo -e "2. Check disk space inside the real pod:"
echo -e "   ${CYAN}kubectl exec -n database-prod postgres-primary-0 -- df -h /var/lib/postgresql/data${NC}"
echo ""
echo -e "3. Query live row counts in the real database:"
echo -e "   ${CYAN}kubectl exec -it -n database-prod postgres-primary-0 -- psql -U postgres -d core_banking -c 'SELECT count(*) FROM transactions;'${NC}"
echo ""
