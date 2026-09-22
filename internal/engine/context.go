package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/drtui/drtui/pkg/pluginapi"
)

type ValueType string

const (
	ValueString     ValueType = "string"
	ValueInteger    ValueType = "integer"
	ValueNumber     ValueType = "number"
	ValueBoolean    ValueType = "boolean"
	ValueIP         ValueType = "ip"
	ValuePath       ValueType = "path"
	ValueDomainList ValueType = "domain-list"
	ValueNetworkMap ValueType = "network-map"
	ValueObject     ValueType = "object"
	ValueArray      ValueType = "array"
	ValueSecret     ValueType = "secret"
)

var ErrOutputAlreadyPublished = errors.New("step output has already been published")

type OutputValue struct {
	Type      ValueType
	JSON      json.RawMessage
	SecretRef *pluginapi.SecretReference
}

func (value OutputValue) Validate() error {
	if value.Type == ValueSecret {
		if value.SecretRef == nil || len(value.JSON) != 0 {
			return errors.New("secret output must contain only a secret reference")
		}
		return nil
	}
	if value.SecretRef != nil || len(value.JSON) == 0 || !json.Valid(value.JSON) {
		return errors.New("non-secret output must contain valid JSON and no secret reference")
	}

	switch value.Type {
	case ValueString, ValuePath:
		var decoded string
		return json.Unmarshal(value.JSON, &decoded)
	case ValueInteger:
		var decoded int64
		return json.Unmarshal(value.JSON, &decoded)
	case ValueNumber:
		var decoded json.Number
		return json.Unmarshal(value.JSON, &decoded)
	case ValueBoolean:
		var decoded bool
		return json.Unmarshal(value.JSON, &decoded)
	case ValueIP:
		var decoded string
		if err := json.Unmarshal(value.JSON, &decoded); err != nil {
			return err
		}
		if _, err := netip.ParseAddr(decoded); err != nil {
			return fmt.Errorf("invalid IP address: %w", err)
		}
		return nil
	case ValueDomainList:
		var decoded []pluginapi.DomainName
		return json.Unmarshal(value.JSON, &decoded)
	case ValueNetworkMap:
		var decoded pluginapi.NetworkMap
		return json.Unmarshal(value.JSON, &decoded)
	case ValueObject:
		var decoded map[string]any
		if err := json.Unmarshal(value.JSON, &decoded); err != nil {
			return err
		}
		if decoded == nil {
			return errors.New("object output cannot be null")
		}
		return nil
	case ValueArray:
		var decoded []any
		if err := json.Unmarshal(value.JSON, &decoded); err != nil {
			return err
		}
		if decoded == nil {
			return errors.New("array output cannot be null")
		}
		return nil
	default:
		return fmt.Errorf("unknown output type %q", value.Type)
	}
}

// StateContext is an in-memory, write-once output store scoped to one run.
// Persisted journals contain metadata and redacted output hashes, not values.
type StateContext struct {
	mu      sync.RWMutex
	outputs map[string]map[string]OutputValue
}

func NewStateContext() *StateContext {
	return &StateContext{outputs: make(map[string]map[string]OutputValue)}
}

func (state *StateContext) Publish(stepID, name string, value OutputValue) error {
	if err := value.Validate(); err != nil {
		return fmt.Errorf("output %s.%s: %w", stepID, name, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.outputs[stepID] == nil {
		state.outputs[stepID] = make(map[string]OutputValue)
	}
	if _, exists := state.outputs[stepID][name]; exists {
		return fmt.Errorf("%w: %s.%s", ErrOutputAlreadyPublished, stepID, name)
	}
	state.outputs[stepID][name] = cloneOutput(value)
	return nil
}

func (state *StateContext) Lookup(stepID, name string) (OutputValue, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	value, ok := state.outputs[stepID][name]
	return cloneOutput(value), ok
}

func cloneOutput(value OutputValue) OutputValue {
	value.JSON = append(json.RawMessage(nil), value.JSON...)
	if value.SecretRef != nil {
		secretRef := *value.SecretRef
		value.SecretRef = &secretRef
	}
	return value
}
