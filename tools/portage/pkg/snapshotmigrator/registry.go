// Package snapshotmigrator provides a pluggable backend-specific snapshot migration framework.
//
// Registry holds all registered SnapshotMigrators and provides lookup by CSI driver.
package snapshotmigrator

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Registry holds all registered SnapshotMigrators.
type Registry struct {
	mu        sync.RWMutex
	migrators map[string]SnapshotMigrator
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		migrators: make(map[string]SnapshotMigrator),
	}
}

// Register adds a SnapshotMigrator to the registry.
// The migrator's Name() is used as the key.
func (r *Registry) Register(m SnapshotMigrator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrators[m.Name()] = m
}

// Get returns the SnapshotMigrator for the given CSI driver name.
// If driverName is empty, returns the first registered migrator (typically same-backend fallback).
func (r *Registry) Get(driverName string) (SnapshotMigrator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if driverName == "" {
		// Return the same-backend migrator as default
		for _, m := range r.migrators {
			if m.Name() == "same-backend" {
				return m, nil
			}
		}
		// Fallback: return first registered
		for _, m := range r.migrators {
			return m, nil
		}
		return nil, fmt.Errorf("no snapshot migrators registered")
	}

	m, ok := r.migrators[driverName]
	if !ok {
		// Fallback to same-backend if available
		if sb, ok := r.migrators["same-backend"]; ok {
			return sb, nil
		}
		return nil, fmt.Errorf("no snapshot migrator registered for CSI driver %q", driverName)
	}
	return m, nil
}

// PickMigrator selects the best migrator for a given PVC and VolumeSnapshotClass.
// It iterates registered migrators and returns the first one whose CanMigrate returns true.
// If no specific migrator matches, returns the same-backend fallback.
func (r *Registry) PickMigrator(pvc *unstructured.Unstructured, snapshotClass string) (SnapshotMigrator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// First pass: find a specific migrator
	for _, m := range r.migrators {
		if m.Name() != "same-backend" && m.CanMigrate(pvc, snapshotClass) {
			return m, nil
		}
	}

	// Fallback to same-backend
	if sb, ok := r.migrators["same-backend"]; ok {
		return sb, nil
	}

	return nil, fmt.Errorf("no suitable snapshot migrator found for PVC with snapshotClass %q", snapshotClass)
}

// ListDrivers returns the names of all registered migrators.
func (r *Registry) ListDrivers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	drivers := make([]string, 0, len(r.migrators))
	for name := range r.migrators {
		drivers = append(drivers, name)
	}
	return drivers
}

// GlobalRegistry is the singleton registry used by the Portage operator.
var GlobalRegistry = func() *Registry {
	r := NewRegistry()
	r.Register(&SameBackendSnapshotMigrator{})
	r.Register(NewEBSSnapshotMigrator("us-east-1", ""))
	return r
}

// MustRegisters all built-in migrators on the given registry.
func MustRegisterAll(r *Registry) {
	r.Register(&SameBackendSnapshotMigrator{})
	r.Register(NewEBSSnapshotMigrator("us-east-1", ""))
}
