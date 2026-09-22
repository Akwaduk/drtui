package pluginapi

import "fmt"

type ErrorCode string

const (
	ErrorInvalidConfig        ErrorCode = "INVALID_CONFIG"
	ErrorAuthenticationFailed ErrorCode = "AUTHENTICATION_FAILED"
	ErrorNotFound             ErrorCode = "NOT_FOUND"
	ErrorConflict             ErrorCode = "CONFLICT"
	ErrorTransient            ErrorCode = "TRANSIENT"
	ErrorTimeout              ErrorCode = "TIMEOUT"
	ErrorInternal             ErrorCode = "INTERNAL"
)

// ProviderError is safe to journal. Details must contain redacted operational
// metadata only; secret values belong in SecretSet and must never be attached.
type ProviderError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Details   map[string]string
	Cause     error
}

func (err *ProviderError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("provider error %s: %s", err.Code, err.Message)
}

func (err *ProviderError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
