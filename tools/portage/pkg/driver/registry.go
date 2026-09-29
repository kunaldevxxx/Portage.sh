package driver

import (
	"fmt"
	"sync"
)

// DriverRegistry coordinates discovery and invocation of replication and cutover drivers.
type DriverRegistry struct {
	mu                 sync.RWMutex
	replicationDrivers map[string]DataReplicationDriver
	cutoverDrivers     map[string]TrafficCutoverDriver
}

var (
	defaultRegistry *DriverRegistry
	once            sync.Once
)

// GlobalRegistry returns the singleton DriverRegistry initialized with baseline Community drivers.
func GlobalRegistry() *DriverRegistry {
	once.Do(func() {
		defaultRegistry = NewDriverRegistry()
		defaultRegistry.RegisterReplicationDriver(NewCommunityReplicationDriver())
		defaultRegistry.RegisterCutoverDriver(NewCommunityCutoverDriver())
	})
	return defaultRegistry
}

func NewDriverRegistry() *DriverRegistry {
	return &DriverRegistry{
		replicationDrivers: make(map[string]DataReplicationDriver),
		cutoverDrivers:     make(map[string]TrafficCutoverDriver),
	}
}

func (r *DriverRegistry) RegisterReplicationDriver(d DataReplicationDriver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replicationDrivers[d.Name()] = d
}

func (r *DriverRegistry) GetReplicationDriver(name string) (DataReplicationDriver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if name == "" || name == "default" || name == "community" {
		name = "community-cdc"
	}

	d, ok := r.replicationDrivers[name]
	if !ok {
		return nil, fmt.Errorf("replication driver %q not registered", name)
	}
	return d, nil
}

func (r *DriverRegistry) RegisterCutoverDriver(d TrafficCutoverDriver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cutoverDrivers[d.Name()] = d
}

func (r *DriverRegistry) GetCutoverDriver(name string) (TrafficCutoverDriver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if name == "" || name == "default" || name == "community" {
		name = "community-cutover"
	}

	d, ok := r.cutoverDrivers[name]
	if !ok {
		return nil, fmt.Errorf("cutover driver %q not registered", name)
	}
	return d, nil
}
