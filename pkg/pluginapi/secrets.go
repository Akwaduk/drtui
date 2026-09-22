package pluginapi

import (
	"errors"
	"sync"
)

var ErrSecretsDestroyed = errors.New("secret set has been destroyed")

// SecretSet owns mutable secret buffers. Call Destroy as soon as the consuming
// step completes. Lookup returns a copy so callers cannot mutate owned data.
// Go cannot guarantee that compiler or runtime copies are wiped; providers
// should avoid converting these values to immutable strings.
type SecretSet struct {
	mu        sync.RWMutex
	values    map[SecretKey][]byte
	destroyed bool
}

func NewSecretSet(values map[SecretKey][]byte) *SecretSet {
	owned := make(map[SecretKey][]byte, len(values))
	for key, value := range values {
		owned[key] = append([]byte(nil), value...)
	}
	return &SecretSet{values: owned}
}

func (set *SecretSet) Lookup(key SecretKey) ([]byte, bool, error) {
	set.mu.RLock()
	defer set.mu.RUnlock()
	if set.destroyed {
		return nil, false, ErrSecretsDestroyed
	}
	value, ok := set.values[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

func (set *SecretSet) Destroy() {
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.destroyed {
		return
	}
	for key, value := range set.values {
		clear(value)
		delete(set.values, key)
	}
	set.destroyed = true
}
