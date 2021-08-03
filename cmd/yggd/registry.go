package main

import (
	"fmt"
	"sync"
)

type registry struct {
	mu sync.RWMutex
	mp map[string]*worker
}

func (r *registry) set(handler string, w *worker) error {
	if r.mp == nil {
		r.mp = make(map[string]*worker)
	}

	if r.get(handler) != nil {
		return fmt.Errorf("cannot add worker; a worker is already registered")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.mp[handler] = w

	return nil
}

func (r *registry) get(handler string) *worker {
	if r.mp == nil {
		r.mp = make(map[string]*worker)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.mp[handler]
}

func (r *registry) del(handler string) {
	if r.mp == nil {
		r.mp = make(map[string]*worker)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.mp, handler)
}

func (r *registry) all() map[string]*worker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := make(map[string]*worker)
	for k, w := range r.mp {
		all[k] = w
	}

	return all
}
