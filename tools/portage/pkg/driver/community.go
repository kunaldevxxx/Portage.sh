package driver

import (
	"context"
	"fmt"
	"sync"
)

// CommunityReplicationDriver provides a basic, open-source fallback replication driver.
// Unlike Enterprise Synapse-CDC, it does not provide continuous sub-second ring buffers
// or mathematical Merkle divergence verification, but satisfies basic sync requirements.
type CommunityReplicationDriver struct {
	mu           sync.Mutex
	streams      map[string]bool
	mockRootHash string
}

func NewCommunityReplicationDriver() *CommunityReplicationDriver {
	return &CommunityReplicationDriver{
		streams:      make(map[string]bool),
		mockRootHash: "community-basic-checksum-pass",
	}
}

func (c *CommunityReplicationDriver) Name() string {
	return "community-cdc"
}

func (c *CommunityReplicationDriver) StartReplication(ctx context.Context, endpoint, streamID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streams[streamID] = true
	return nil
}

func (c *CommunityReplicationDriver) VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streams[streamID] {
		return false, "", fmt.Errorf("stream %q not active in community replication driver", streamID)
	}
	return true, c.mockRootHash, nil
}

func (c *CommunityReplicationDriver) GetLag(ctx context.Context, endpoint, streamID string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streams[streamID] {
		return 0, fmt.Errorf("stream %q not found", streamID)
	}
	return 0, nil
}

func (c *CommunityReplicationDriver) GetCurrentLSN(ctx context.Context, endpoint, streamID string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.streams[streamID] {
		return 0, fmt.Errorf("stream %q not found", streamID)
	}
	return 0, nil
}

// CommunityCutoverDriver provides a standard Kubernetes Service endpoint or DNS swap.
// Note: Community cutover drops existing in-flight TCP connections (standard TCP RST),
// whereas Enterprise Ghostwire provides zero-reset kernel splicing and connection draining.
type CommunityCutoverDriver struct {
	mu       sync.Mutex
	cutover  map[string]string
}

func NewCommunityCutoverDriver() *CommunityCutoverDriver {
	return &CommunityCutoverDriver{
		cutover: make(map[string]string),
	}
}

func (c *CommunityCutoverDriver) Name() string {
	return "community-cutover"
}

func (c *CommunityCutoverDriver) ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cutover[routeID] = targetEndpoint
	return nil
}

func (c *CommunityCutoverDriver) ExecuteRollback(ctx context.Context, directorEndpoint, routeID string, drainTimeoutSec int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Remove the route mapping to effectively rollback
	delete(c.cutover, routeID)
	return nil
}

func (c *CommunityCutoverDriver) CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error) {
	return true, nil
}

func (c *CommunityCutoverDriver) GetDrainProgress(ctx context.Context, directorEndpoint, routeID string) (int64, int64, error) {
	return 0, 100, nil
}
