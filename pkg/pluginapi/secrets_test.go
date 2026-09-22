package pluginapi

import (
	"errors"
	"testing"
)

func TestSecretSetOwnsInputAndDestroysValues(t *testing.T) {
	original := []byte("temporary-secret")
	secrets := NewSecretSet(map[SecretKey][]byte{"APP_KEY": original})
	original[0] = 'X'

	value, ok, err := secrets.Lookup("APP_KEY")
	if err != nil || !ok || string(value) != "temporary-secret" {
		t.Fatalf("Lookup() = %q, %v, %v", value, ok, err)
	}

	secrets.Destroy()
	if _, _, err := secrets.Lookup("APP_KEY"); !errors.Is(err, ErrSecretsDestroyed) {
		t.Fatalf("Lookup() after Destroy() error = %v", err)
	}
}
