package proxmoxpbs

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/drtui/drtui/pkg/pluginapi"
)

func TestIntegrationPBSRepository(t *testing.T) {
	if os.Getenv("DRTUI_PBS_TEST") != "1" {
		t.Skip("set DRTUI_PBS_TEST=1 to test a live PBS repository")
	}
	port := uint16(8007)
	if value := os.Getenv("DRTUI_PBS_PORT"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			t.Fatalf("DRTUI_PBS_PORT: %v", err)
		}
		port = uint16(parsed)
	}
	required := func(name string) string {
		t.Helper()
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is required when DRTUI_PBS_TEST=1", name)
		}
		return value
	}

	client, err := New(Config{
		ClientPath:    os.Getenv("DRTUI_PBS_CLIENT_PATH"),
		Server:        required("DRTUI_PBS_SERVER"),
		Port:          port,
		Datastore:     required("DRTUI_PBS_DATASTORE"),
		AuthID:        required("DRTUI_PBS_AUTH_ID"),
		Namespace:     os.Getenv("DRTUI_PBS_NAMESPACE"),
		Fingerprint:   required("DRTUI_PBS_FINGERPRINT"),
		Password:      pluginapi.SecretReference{Provider: "integration", Key: "pbs-token"},
		WorkDirectory: t.TempDir(),
	}, nil, staticSecrets{value: []byte(required("DRTUI_PBS_TOKEN"))})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := client.ProbeRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Reachable {
		t.Fatal("PBS repository probe did not report reachable")
	}

	snapshots, err := client.ListSnapshots(ctx, pluginapi.BackupSnapshotQuery{
		Group:     os.Getenv("DRTUI_PBS_GROUP"),
		Namespace: os.Getenv("DRTUI_PBS_NAMESPACE"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PBS reachable; server version %q; snapshots visible: %d", status.ServerVersion, len(snapshots))
}
