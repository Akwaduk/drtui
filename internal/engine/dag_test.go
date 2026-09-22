package engine

import (
	"errors"
	"reflect"
	"testing"
)

func TestTopologicalOrderIsStable(t *testing.T) {
	steps := []Step{
		{ID: "secrets"},
		{ID: "provision"},
		{ID: "download", Needs: []string{"secrets"}},
		{ID: "bootstrap", Needs: []string{"provision", "download"}},
	}

	got, err := TopologicalOrder(steps)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"secrets", "provision", "download", "bootstrap"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TopologicalOrder() = %v, want %v", got, want)
	}
}

func TestTopologicalOrderRejectsCycles(t *testing.T) {
	steps := []Step{
		{ID: "one", Needs: []string{"two"}},
		{ID: "two", Needs: []string{"one"}},
	}
	if _, err := TopologicalOrder(steps); !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("TopologicalOrder() error = %v", err)
	}
}

func TestRollbackOrderIsReverseTopological(t *testing.T) {
	steps := []Step{
		{ID: "provision", Transaction: "recovery", Rollback: &RollbackDefinition{}},
		{ID: "bootstrap", Needs: []string{"provision"}, Transaction: "recovery", Rollback: &RollbackDefinition{}},
		{ID: "dns", Needs: []string{"bootstrap"}, Transaction: "recovery", Rollback: &RollbackDefinition{}},
	}
	succeeded := map[string]bool{"provision": true, "bootstrap": true, "dns": true}

	got, err := RollbackOrder(steps, "recovery", succeeded)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dns", "bootstrap", "provision"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RollbackOrder() = %v, want %v", got, want)
	}
}

func TestOutputValueValidatesDeclaredType(t *testing.T) {
	valid := OutputValue{Type: ValueIP, JSON: []byte(`"203.0.113.10"`)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid IP output rejected: %v", err)
	}

	invalid := OutputValue{Type: ValueIP, JSON: []byte(`"not-an-ip"`)}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid IP output accepted")
	}
}
