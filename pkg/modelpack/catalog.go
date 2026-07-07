package modelpack

import (
	"context"
	"fmt"
	"sync"
)

// ModelCatalog provides a fleet-wide view of available models.
type ModelCatalog interface {
	List(ctx context.Context) ([]ModelPackConfig, error)
	Get(ctx context.Context, name string) (*ModelPackConfig, error)
	Register(ctx context.Context, ociRef string) (*ModelPackConfig, error)
	Deregister(ctx context.Context, name string) error
}

// InMemoryModelCatalog is a simple in-memory implementation of ModelCatalog
// suitable for development and testing. Production use should back this with
// a persistent store.
type InMemoryModelCatalog struct {
	mu       sync.RWMutex
	models   map[string]*ModelPackConfig // keyed by model name
	resolver ModelResolver
}

// NewInMemoryModelCatalog creates a new InMemoryModelCatalog using the given
// resolver to fetch ModelPack configs during registration.
func NewInMemoryModelCatalog(resolver ModelResolver) *InMemoryModelCatalog {
	return &InMemoryModelCatalog{
		models:   make(map[string]*ModelPackConfig),
		resolver: resolver,
	}
}

// List returns all registered models.
func (c *InMemoryModelCatalog) List(_ context.Context) ([]ModelPackConfig, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]ModelPackConfig, 0, len(c.models))
	for _, m := range c.models {
		result = append(result, *m)
	}
	return result, nil
}

// Get returns a specific model by name.
func (c *InMemoryModelCatalog) Get(_ context.Context, name string) (*ModelPackConfig, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	m, ok := c.models[name]
	if !ok {
		return nil, fmt.Errorf("model %q not found in catalog", name)
	}
	return m, nil
}

// Register resolves a ModelPack config from the OCI reference and adds it to
// the catalog. If the resolver is available and succeeds, the resolved config
// is stored; otherwise registration stores a placeholder that records the
// OCI reference for later resolution.
func (c *InMemoryModelCatalog) Register(ctx context.Context, ociRef string) (*ModelPackConfig, error) {
	if ociRef == "" {
		return nil, fmt.Errorf("ociRef must not be empty")
	}

	var config *ModelPackConfig

	if c.resolver != nil {
		resolved, err := c.resolver.Resolve(ctx, ociRef)
		if err == nil {
			config = resolved
		}
	}

	// If resolution failed or no resolver, create a placeholder entry.
	if config == nil {
		config = &ModelPackConfig{
			OciRef: ociRef,
			Descriptor: ModelDescriptor{
				Name: ociRef, // Use the ref as the name until resolved.
			},
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.models[config.Descriptor.Name] = config
	return config, nil
}

// Deregister removes a model from the catalog by name.
func (c *InMemoryModelCatalog) Deregister(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.models[name]; !ok {
		return fmt.Errorf("model %q not found in catalog", name)
	}
	delete(c.models, name)
	return nil
}
