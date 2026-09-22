package engine

import "encoding/json"

const (
	APIVersionV1Alpha1       = "drtui.io/v1alpha1"
	KindDisasterRecoveryPlan = "DisasterRecoveryPlan"
)

type Plan struct {
	APIVersion   string                     `json:"apiVersion"`
	Kind         string                     `json:"kind"`
	Metadata     PlanMetadata               `json:"metadata"`
	Inputs       map[string]InputDefinition `json:"inputs,omitempty"`
	Providers    map[string]ProviderBinding `json:"providers"`
	Transactions map[string]Transaction     `json:"transactions,omitempty"`
	Policy       ExecutionPolicy            `json:"policy"`
	Steps        []Step                     `json:"steps"`
}

type PlanMetadata struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type InputDefinition struct {
	Type        ValueType       `json:"type"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
}

type ProviderBinding struct {
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Version string          `json:"version"`
	Digest  string          `json:"digest"`
	Config  json.RawMessage `json:"config,omitempty"`
}

type Transaction struct {
	Rollback string `json:"rollback"`
}

type ExecutionPolicy struct {
	MaxParallel       uint16 `json:"maxParallel"`
	FailFast          bool   `json:"failFast"`
	RequireSignatures bool   `json:"requireSignatures"`
	AllowUnpinned     bool   `json:"allowUnpinned"`
	WorkDirectory     string `json:"workDirectory"`
}

type Step struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Uses        string                      `json:"uses"`
	Needs       []string                    `json:"needs,omitempty"`
	Transaction string                      `json:"transaction,omitempty"`
	Condition   string                      `json:"if,omitempty"`
	ForEach     string                      `json:"forEach,omitempty"`
	With        json.RawMessage             `json:"with,omitempty"`
	Outputs     map[string]OutputDefinition `json:"outputs,omitempty"`
	Timeout     string                      `json:"timeout"`
	Retry       RetryPolicy                 `json:"retry,omitempty"`
	Rollback    *RollbackDefinition         `json:"rollback,omitempty"`
}

type OutputDefinition struct {
	Type      ValueType `json:"type"`
	From      string    `json:"from"`
	Sensitive bool      `json:"sensitive,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts uint16 `json:"maxAttempts,omitempty"`
	Backoff     string `json:"backoff,omitempty"`
	MaxBackoff  string `json:"maxBackoff,omitempty"`
	Jitter      bool   `json:"jitter,omitempty"`
}

type RollbackDefinition struct {
	Uses    string          `json:"uses"`
	With    json.RawMessage `json:"with,omitempty"`
	Timeout string          `json:"timeout"`
	Retry   RetryPolicy     `json:"retry,omitempty"`
}
