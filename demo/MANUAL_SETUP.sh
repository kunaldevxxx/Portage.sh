#!/bin/bash
# Manual step-by-step setup - ~512 MB demo dataset
# Run each section one at a time

set -e

echo "=== Manual Portage Demo Setup (~512 MB) ==="
echo ""

# STEP 1: Create clusters
echo "STEP 1: Creating kind clusters..."
echo "Deleting old clusters..."
kind delete cluster --name source-prod 2>/dev/null || true
kind delete cluster --name target-modern 2>/dev/null || true
sleep 3

echo "Creating source-prod..."
kind create cluster --name source-prod --image kindest/node:v1.27.0
echo "✓ source-prod created"

echo "Creating target-modern..."
kind create cluster --name target-modern --image kindest/node:v1.27.0
echo "✓ target-modern created"

sleep 5
echo ""

# STEP 2: Deploy PostgreSQL to source
echo "STEP 2: Deploying PostgreSQL to source cluster..."
kubectl config use-context kind-source-prod
kubectl create namespace database-prod || true

cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
  namespace: database-prod
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres-primary
  namespace: database-prod
spec:
  serviceName: postgres-service
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
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_DB
          value: "production"
        - name: POSTGRES_USER
          value: "postgres"
        - name: POSTGRES_PASSWORD
          value: "securepassword123"
        volumeMounts:
        - name: postgres-storage
          mountPath: /var/lib/postgresql/data
      volumes:
      - name: postgres-storage
        persistentVolumeClaim:
          claimName: postgres-data
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-service
  namespace: database-prod
spec:
  ports:
  - port: 5432
    targetPort: 5432
  clusterIP: None
  selector:
    app: postgres
EOF

echo "Waiting for PostgreSQL to start..."
kubectl wait --for=condition=ready pod -l app=postgres \
  -n database-prod --timeout=180s

echo "✓ Source PostgreSQL ready"
echo ""

# STEP 3: Deploy PostgreSQL to target
echo "STEP 3: Deploying PostgreSQL to target cluster..."
kubectl config use-context kind-target-modern
kubectl create namespace database-modern || true

cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
  namespace: database-modern
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres-primary
  namespace: database-modern
spec:
  serviceName: postgres-service
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
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_DB
          value: "production"
        - name: POSTGRES_USER
          value: "postgres"
        - name: POSTGRES_PASSWORD
          value: "securepassword123"
        volumeMounts:
        - name: postgres-storage
          mountPath: /var/lib/postgresql/data
      volumes:
      - name: postgres-storage
        persistentVolumeClaim:
          claimName: postgres-data
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-service
  namespace: database-modern
spec:
  ports:
  - port: 5432
    targetPort: 5432
  clusterIP: None
  selector:
    app: postgres
EOF

echo "Waiting for target PostgreSQL to start..."
kubectl wait --for=condition=ready pod -l app=postgres \
  -n database-modern --timeout=180s

echo "✓ Target PostgreSQL ready"
echo ""

# STEP 4: Generate ~512 MB test data
echo "STEP 4: Setting up port forward and generating data..."

kubectl config use-context kind-source-prod

kubectl port-forward -n database-prod \
  svc/postgres-service 5432:5432 >/dev/null 2>&1 &

PORTFORWARD_PID=$!

trap 'kill $PORTFORWARD_PID 2>/dev/null || true' EXIT

sleep 3

echo "Creating table and populating with test data..."
echo "Target: approximately 512 MB"
echo "This should take a few minutes..."
echo "☕"

PGPASSWORD=securepassword123 psql \
  -h localhost \
  -U postgres \
  -d production <<'ENDSQL'

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS transaction_ledger (
    id BIGSERIAL PRIMARY KEY,
    account_id UUID NOT NULL,
    transaction_id UUID NOT NULL UNIQUE,
    amount DECIMAL(19,4) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    description TEXT,
    transaction_date TIMESTAMP NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_account_id
    ON transaction_ledger(account_id);

CREATE INDEX IF NOT EXISTS idx_transaction_date
    ON transaction_ledger(transaction_date);

-- Generate approximately 512 MB of test data.
-- 6 million rows is a reasonable starting point for this schema.
DO $$
DECLARE
    batch_size INTEGER := 5000;
    total_rows INTEGER := 6000000;
    i INTEGER := 0;
BEGIN
    WHILE i < total_rows LOOP

        INSERT INTO transaction_ledger
            (
                account_id,
                transaction_id,
                amount,
                currency,
                description,
                transaction_date,
                metadata
            )
        SELECT
            gen_random_uuid(),
            gen_random_uuid(),
            (random() * 10000)::DECIMAL(19,4),

            CASE (random() * 3)::INT
                WHEN 0 THEN 'USD'
                WHEN 1 THEN 'EUR'
                WHEN 2 THEN 'GBP'
                ELSE 'JPY'
            END,

            substring(md5(random()::text), 1, 50),

            CURRENT_TIMESTAMP -
                (random() * INTERVAL '365 days'),

            jsonb_build_object(
                'merchant',
                'M_' || (random() * 10000)::INT
            )

        FROM generate_series(1, batch_size);

        i := i + batch_size;

        IF i % 500000 = 0 THEN
            RAISE NOTICE 'Inserted % rows', i;
        END IF;

    END LOOP;
END $$;

SELECT
    COUNT(*) AS total_rows,
    pg_size_pretty(
        pg_total_relation_size('transaction_ledger')
    ) AS total_size;

ENDSQL

echo ""
echo "✓ Data generation complete"

# Final verification while port-forward is still running
echo ""
echo "=== Final Verification ==="

PGPASSWORD=securepassword123 psql \
  -h localhost \
  -U postgres \
  -d production \
  -c "
    SELECT
      COUNT(*) AS rows,
      pg_size_pretty(
        pg_total_relation_size('transaction_ledger')
      ) AS size
    FROM transaction_ledger;
  "

# Stop port-forward
kill $PORTFORWARD_PID 2>/dev/null || true
trap - EXIT

echo "=== STEP 5: Installing CRDs & Building Portage ==="

cd /Users/kunal7ds/Portage.sh

kubectl --context kind-source-prod apply -f deploy/crds/migration.portage.sh_portagemigrations.yaml
kubectl --context kind-target-modern apply -f deploy/crds/migration.portage.sh_portagemigrations.yaml
echo "✓ Portage CRDs installed"

# Create /tmp/live-demo-migration.yaml
cat <<'EOF' > /tmp/live-demo-migration.yaml
apiVersion: migration.portage.sh/v1alpha1
kind: PortageMigration
metadata:
  name: postgres-live-demo
  namespace: database-prod
spec:
  sourceWorkload:
    kind: StatefulSet
    name: postgres-primary
  sourceNamespace: database-prod
  targetNamespace: database-modern

  preQuiesceHooks:
    - name: postgres-checkpoint
      type: PodExec
      timeoutSeconds: 30
      maxRetries: 3
      podExec:
        selector:
          matchLabels:
            app: postgres
        command:
          - psql
          - -U
          - postgres
          - -d
          - production
          - -c
          - "CHECKPOINT; SELECT pg_backup_start('portage_live_demo', true);"

  postQuiesceHooks:
    - name: postgres-unlock
      type: PodExec
      timeoutSeconds: 30
      maxRetries: 3
      podExec:
        selector:
          matchLabels:
            app: postgres
        command:
          - psql
          - -U
          - postgres
          - -d
          - production
          - -c
          - "SELECT pg_backup_stop();"

  cdcReplication:
    driver: enterprise-synapse
    enabled: true
    endpoint: "http://synapse-cdc.migration.svc:9000"
    streamID: "postgres-primary-wal"
    requireMerkleVerification: true
    maxAllowedLagSeconds: 2

  trafficCutover:
    driver: enterprise-ghostwire
    enabled: true
    directorEndpoint: "http://ghostwire.migration.svc:8000"
    routeID: "postgres-db-route"
    targetEndpoint: "postgres-service.database-modern.svc:5432"
    drainTimeoutSeconds: 15

  volumeMigration:
    volumeSnapshotClassName: standard
    targetStorageClassName: standard
    persistentVolumeClaimNames:
      - postgres-data
    retainOnSource: true
EOF
echo "✓ Generated /tmp/live-demo-migration.yaml"

go build -o bin/portage ./tools/portage

echo "✓ Portage built"
echo ""

echo "=== Setup Complete! ==="
echo ""
echo "✅ ~512 MB demo dataset is ready!"
echo ""
echo "Next steps:"
echo ""
echo "1. Start dashboard:"
echo "   ./bin/portage dashboard -p 8080 &"
echo ""
echo "2. Open:"
echo "   http://localhost:8080"
echo ""
echo "3. In a new terminal:"
echo "   kubectl config use-context kind-source-prod"
echo "   kubectl get portagemigration -n database-prod -w"
echo ""
echo "4. Apply migration:"
echo "   kubectl apply -f /tmp/live-demo-migration.yaml"
echo ""