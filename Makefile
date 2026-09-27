.PHONY: all test test-portage test-synapse test-ghostwire lint build clean

all: build test

build:
	@echo "==> Building Portage.sh (Go)..."
	cd tools/portage && go build -v ./...
	@echo "==> Building Synapse-CDC (Rust)..."
	cargo build --package synapse-cdc
	@echo "==> Building Ghostwire (Rust)..."
	cargo build --package ghostwire

test: test-portage test-synapse test-ghostwire

test-portage:
	@echo "==> Running Portage.sh tests (Go)..."
	cd tools/portage && go test -v -race -cover ./...

test-synapse:
	@echo "==> Running Synapse-CDC tests (Rust)..."
	cargo test --package synapse-cdc

test-ghostwire:
	@echo "==> Running Ghostwire tests (Rust)..."
	cargo test --package ghostwire

clean:
	@echo "==> Cleaning artifacts..."
	cargo clean
	cd tools/portage && go clean
