.PHONY: all test test-portage lint build clean install-crds deploy undeploy

all: build test

build:
	@echo "==> Building Portage.sh Kubernetes Operator (Go)..."
	cd tools/portage && go build -v ./...

test: test-portage

test-portage:
	@echo "==> Running Portage.sh tests (Go)..."
	cd tools/portage && go test -v -race -cover ./...

clean:
	@echo "==> Cleaning artifacts..."
	cd tools/portage && go clean

install-crds:
	@echo "==> Installing PortageMigration CRDs..."
	kubectl apply -f deploy/crds/migration.portage.sh_portagemigrations.yaml

deploy: install-crds
	@echo "==> Deploying Portage Operator..."
	kubectl apply -f deploy/operator.yaml

undeploy:
	@echo "==> Removing Portage Operator & CRDs..."
	kubectl delete -f deploy/operator.yaml --ignore-not-found
	kubectl delete -f deploy/crds/migration.portage.sh_portagemigrations.yaml --ignore-not-found
