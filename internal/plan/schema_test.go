package plan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drtui/drtui/internal/engine"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v4"
)

func TestCoolifyExampleConformsToSchemaAndDAG(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	validatePlan(t, repositoryRoot, filepath.Join("examples", "coolify-s3-cloudflare", "plan.drp.yaml"))
}

func TestProxmoxPBSExampleConformsToSchemaAndDAG(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	validatePlan(t, repositoryRoot, filepath.Join("examples", "proxmox-pbs", "plan.drp.yaml"))
}

func validatePlan(t *testing.T, repositoryRoot, relativePlanPath string) {
	t.Helper()
	planBytes := readFile(t, filepath.Join(repositoryRoot, relativePlanPath))
	schema := compileSchema(t, filepath.Join(repositoryRoot, "schemas", "drp.schema.json"))

	var document any
	if err := yaml.Unmarshal(planBytes, &document); err != nil {
		t.Fatalf("parse example plan: %v", err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("validate example plan: %v", err)
	}

	canonicalJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("convert example plan to JSON: %v", err)
	}
	var recoveryPlan engine.Plan
	if err := json.Unmarshal(canonicalJSON, &recoveryPlan); err != nil {
		t.Fatalf("decode typed example plan: %v", err)
	}
	order, err := engine.TopologicalOrder(recoveryPlan.Steps)
	if err != nil {
		t.Fatalf("compile example DAG: %v", err)
	}
	if len(order) != len(recoveryPlan.Steps) {
		t.Fatalf("compiled %d of %d plan steps", len(order), len(recoveryPlan.Steps))
	}
}

func TestPluginSchemaCompiles(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	compileSchema(t, filepath.Join(repositoryRoot, "schemas", "plugin.schema.json"))
}

func TestProxmoxPBSManifestConformsToPluginSchema(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	schema := compileSchema(t, filepath.Join(repositoryRoot, "schemas", "plugin.schema.json"))
	manifestPath := filepath.Join(repositoryRoot, "plugins", "proxmoxpbs", "manifest.json")
	var manifest any
	if err := json.Unmarshal(readFile(t, manifestPath), &manifest); err != nil {
		t.Fatalf("parse PBS plugin manifest: %v", err)
	}
	if err := schema.Validate(manifest); err != nil {
		t.Fatalf("validate PBS plugin manifest: %v", err)
	}
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	var schemaDocument any
	if err := json.Unmarshal(readFile(t, path), &schemaDocument); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(path, schemaDocument); err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	schema, err := compiler.Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	return schema
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}
