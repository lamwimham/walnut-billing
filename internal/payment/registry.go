package payment

import (
	"fmt"
	"sync"
)

// ProviderRegistry is a thread-safe registry of payment providers.
// Supports hot-swapping providers at runtime without server restart.
// Uses the Registry pattern with RWMutex for concurrent reads.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]PaymentProvider
	metadata  map[string]ProviderStatus
}

// ProviderStatus holds runtime status info about a provider.
type ProviderStatus struct {
	Name        string `json:"name"`
	IsMock      bool   `json:"is_mock"`
	SandboxMode bool   `json:"sandbox_mode"`
	NotifyURL   string `json:"notify_url"`
	Status      string `json:"status"` // "active", "error"
}

// NewProviderRegistry creates an empty registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]PaymentProvider),
		metadata:  make(map[string]ProviderStatus),
	}
}

// Register adds or replaces a provider in the registry.
func (r *ProviderRegistry) Register(name string, provider PaymentProvider, status ProviderStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = provider
	status.Name = name
	status.Status = "active"
	r.metadata[name] = status
}

// Get returns a provider by name.
func (r *ProviderRegistry) Get(name string) (PaymentProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, name)
	}
	return provider, nil
}

// List returns all registered provider names.
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// Status returns the runtime status of all providers.
func (r *ProviderRegistry) Status() map[string]ProviderStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]ProviderStatus, len(r.metadata))
	for k, v := range r.metadata {
		result[k] = v
	}
	return result
}

// HasProvider checks if a provider exists.
func (r *ProviderRegistry) HasProvider(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[name]
	return ok
}

// Remove removes a provider from the registry.
func (r *ProviderRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, name)
	delete(r.metadata, name)
}
