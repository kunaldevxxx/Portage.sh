package driver

import (
	"context"
	"testing"
)

func TestCommunityReplicationDriverLifecycle(t *testing.T) {
	d := NewCommunityReplicationDriver()
	if d.Name() != "community-cdc" {
		t.Errorf("expected name community-cdc, got %s", d.Name())
	}

	ctx := context.Background()
	streamID := "test-stream-101"

	// Verify before start should fail
	if _, _, err := d.VerifyState(ctx, "http://localhost:8080", streamID); err == nil {
		t.Error("expected error verifying unstarted stream, got nil")
	}

	if err := d.StartReplication(ctx, "http://localhost:8080", streamID); err != nil {
		t.Fatalf("unexpected error starting replication: %v", err)
	}

	verified, hash, err := d.VerifyState(ctx, "http://localhost:8080", streamID)
	if err != nil {
		t.Fatalf("unexpected error verifying state: %v", err)
	}
	if !verified || hash == "" {
		t.Errorf("expected verified=true with valid hash, got %v, %s", verified, hash)
	}

	lag, err := d.GetLag(ctx, "http://localhost:8080", streamID)
	if err != nil {
		t.Fatalf("unexpected error getting lag: %v", err)
	}
	if lag != 0 {
		t.Errorf("expected lag 0, got %d", lag)
	}
}

func TestCommunityCutoverDriverLifecycle(t *testing.T) {
	d := NewCommunityCutoverDriver()
	if d.Name() != "community-cutover" {
		t.Errorf("expected name community-cutover, got %s", d.Name())
	}

	ctx := context.Background()
	healthy, err := d.CheckTargetHealth(ctx, "http://director:9090", "target:5432")
	if err != nil || !healthy {
		t.Errorf("expected target healthy=true, got %v, %v", healthy, err)
	}

	if err := d.ExecuteCutover(ctx, "http://director:9090", "route-1", "target:5432", 10); err != nil {
		t.Fatalf("unexpected cutover failure: %v", err)
	}
}

func TestDriverRegistryResolution(t *testing.T) {
	reg := NewDriverRegistry()
	reg.RegisterReplicationDriver(NewCommunityReplicationDriver())
	reg.RegisterCutoverDriver(NewCommunityCutoverDriver())

	// Test default / empty fallback
	repDriver, err := reg.GetReplicationDriver("")
	if err != nil {
		t.Fatalf("failed resolving default replication driver: %v", err)
	}
	if repDriver.Name() != "community-cdc" {
		t.Errorf("expected community-cdc, got %s", repDriver.Name())
	}

	cutDriver, err := reg.GetCutoverDriver("default")
	if err != nil {
		t.Fatalf("failed resolving default cutover driver: %v", err)
	}
	if cutDriver.Name() != "community-cutover" {
		t.Errorf("expected community-cutover, got %s", cutDriver.Name())
	}

	// Test unknown driver
	if _, err := reg.GetReplicationDriver("non-existent"); err == nil {
		t.Error("expected error for non-existent driver, got nil")
	}
}
