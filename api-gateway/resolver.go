package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"transmision/pkg/consul"
)

// ErrNoHealthyInstances se devuelve cuando Consul no tiene instancias "passing".
var ErrNoHealthyInstances = errors.New("no hay instancias saludables registradas en Consul")

// Resolver resuelve nombres lógicos de servicio a instancias concretas usando
// Consul. Mantiene una caché de corta duración para no consultar a Consul en
// cada petición y reparte la carga entre réplicas con round-robin.
type Resolver struct {
	client *consul.Client
	ttl    time.Duration

	mu       sync.Mutex
	cache    map[string]cacheEntry
	counters map[string]*atomic.Uint64
}

type cacheEntry struct {
	instances []consul.Instance
	expires   time.Time
}

// NewResolver crea un resolver con el TTL de caché indicado.
func NewResolver(client *consul.Client, ttl time.Duration) *Resolver {
	return &Resolver{
		client:   client,
		ttl:      ttl,
		cache:    make(map[string]cacheEntry),
		counters: make(map[string]*atomic.Uint64),
	}
}

// Pick devuelve la siguiente instancia saludable del servicio (round-robin).
func (r *Resolver) Pick(ctx context.Context, service string) (consul.Instance, error) {
	instances, err := r.instances(ctx, service)
	if err != nil {
		return consul.Instance{}, err
	}
	if len(instances) == 0 {
		return consul.Instance{}, ErrNoHealthyInstances
	}

	r.mu.Lock()
	counter, ok := r.counters[service]
	if !ok {
		counter = new(atomic.Uint64)
		r.counters[service] = counter
	}
	r.mu.Unlock()

	n := counter.Add(1) - 1
	return instances[n%uint64(len(instances))], nil
}

// Invalidate descarta la caché de un servicio (p. ej. tras un error de proxy).
func (r *Resolver) Invalidate(service string) {
	r.mu.Lock()
	delete(r.cache, service)
	r.mu.Unlock()
}

func (r *Resolver) instances(ctx context.Context, service string) ([]consul.Instance, error) {
	r.mu.Lock()
	entry, ok := r.cache[service]
	r.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.instances, nil
	}

	instances, err := r.client.HealthyInstances(ctx, service)
	if err != nil {
		// Si Consul falla momentáneamente, servimos la última lista conocida.
		if ok && len(entry.instances) > 0 {
			return entry.instances, nil
		}
		return nil, err
	}

	r.mu.Lock()
	r.cache[service] = cacheEntry{instances: instances, expires: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return instances, nil
}
