package driver

import (
	"context"
)

// DataReplicationDriver abstracts data-plane continuous replication engines.
// Open-Core allows community fallback while supporting enterprise zero-divergence engines.
type DataReplicationDriver interface {
	// Name returns the unique driver identifier (e.g., "community-cdc", "enterprise-synapse")
	Name() string
	// StartReplication initiates continuous data streaming for the given stream ID
	StartReplication(ctx context.Context, endpoint, streamID string) error
	// VerifyState compares source and target cryptographic state proofs
	VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error)
	// GetLag returns the current replication lag in seconds
	GetLag(ctx context.Context, endpoint, streamID string) (int64, error)
	// GetCurrentLSN returns the last replicated WAL/sequence LSN
	GetCurrentLSN(ctx context.Context, endpoint, streamID string) (uint64, error)
}

// TrafficCutoverDriver abstracts traffic-plane routing and connection draining proxies.
// Open-Core allows community fallback while supporting enterprise zero-RST splicing engines.
type TrafficCutoverDriver interface {
	// Name returns the unique driver identifier (e.g., "community-cutover", "enterprise-ghostwire")
	Name() string
	// ExecuteCutover performs the traffic cutover to the target endpoint and drains active connections
	ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error
	// ExecuteRollback performs the traffic rollback to the source endpoint and drains target connections
	ExecuteRollback(ctx context.Context, directorEndpoint, routeID string, drainTimeoutSec int32) error
	// CheckTargetHealth validates backend connectivity prior to swapping traffic
	CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error)
	// GetDrainProgress returns active connection count and drain progress percentage
	GetDrainProgress(ctx context.Context, directorEndpoint, routeID string) (int64, int64, error)
}
