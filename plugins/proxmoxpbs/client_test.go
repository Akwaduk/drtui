package proxmoxpbs

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/drtui/drtui/pkg/pluginapi"
)

const testFingerprint = "AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA"

type staticSecrets struct {
	value []byte
}

func (secrets staticSecrets) Resolve(context.Context, pluginapi.SecretReference) ([]byte, error) {
	return append([]byte(nil), secrets.value...), nil
}

type scriptedRunner struct {
	results  []CommandResult
	errors   []error
	commands []Command
}

func (runner *scriptedRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	command.Args = append([]string(nil), command.Args...)
	command.Stdin = append([]byte(nil), command.Stdin...)
	runner.commands = append(runner.commands, command)
	index := len(runner.commands) - 1
	return runner.results[index], runner.errors[index]
}

func TestProbeRepositoryUsesPinnedNonInteractiveAuthentication(t *testing.T) {
	runner := &scriptedRunner{
		results: []CommandResult{
			{Stdout: []byte(`{"client-version":"3.4.2","server-version":"3.4.1"}`)},
			{Stdout: []byte(`{"total":1000,"used":400,"avail":600}`)},
		},
		errors: []error{nil, nil},
	}
	client := newTestClient(t, runner)

	status, err := client.ProbeRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Reachable || status.ServerVersion != "3.4.1" || status.AvailableBytes != 600 {
		t.Fatalf("unexpected status: %#v", status)
	}
	for _, command := range runner.commands {
		if command.Env["PBS_PASSWORD_FD"] != "0" || command.Env["PBS_FINGERPRINT"] != testFingerprint {
			t.Fatalf("missing protected PBS environment: %#v", command.Env)
		}
		if string(command.Stdin) != "api-token-secret\n" {
			t.Fatalf("unexpected credential input: %q", command.Stdin)
		}
		joined := strings.Join(command.Args, " ") + strings.Join(mapValues(command.Env), " ")
		if strings.Contains(joined, "api-token-secret") {
			t.Fatal("credential leaked into command arguments or environment")
		}
	}
}

func TestListSnapshotsBuildsComponentCommandAndParsesJSON(t *testing.T) {
	runner := &scriptedRunner{
		results: []CommandResult{{Stdout: []byte(`[{"snapshot":"vm/101/2026-09-21T08:30:00Z","size":4096,"files":["qemu-server.conf.blob","drive-scsi0.img.fidx"]}]`)}},
		errors:  []error{nil},
	}
	client := newTestClient(t, runner)

	snapshots, err := client.ListSnapshots(context.Background(), pluginapi.BackupSnapshotQuery{Group: "vm/101", Namespace: "dr/site-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Group != "vm/101" || snapshots[0].SizeBytes != 4096 {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
	wantPrefix := []string{"snapshot", "list", "vm/101", "--output-format", "json"}
	if !reflect.DeepEqual(runner.commands[0].Args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("command prefix = %v, want %v", runner.commands[0].Args, wantPrefix)
	}
	if !containsPair(runner.commands[0].Args, "--ns", "dr/site-a") {
		t.Fatalf("namespace missing from %v", runner.commands[0].Args)
	}
}

func TestRestoreArchiveConfinesDestination(t *testing.T) {
	runner := &scriptedRunner{results: []CommandResult{{}}, errors: []error{nil}}
	client := newTestClient(t, runner)
	request := pluginapi.RestoreArchiveRequest{
		Snapshot:         pluginapi.BackupSnapshotReference{ID: "vm/101/2026-09-21T08:30:00Z"},
		Archive:          "drive-scsi0.img.fidx",
		LocalDestination: "restored/disk.img",
	}

	result, err := client.RestoreArchive(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(result.LocalDestination), client.config.WorkDirectory+string(filepath.Separator)) {
		t.Fatalf("destination escaped work directory: %s", result.LocalDestination)
	}

	request.LocalDestination = "../outside.img"
	if _, err := client.RestoreArchive(context.Background(), request); err == nil {
		t.Fatal("restore accepted a path outside its work directory")
	}
}

func TestCommandFailureMapsAuthenticationWithoutStderrLeak(t *testing.T) {
	runner := &scriptedRunner{
		results: []CommandResult{{Stderr: []byte("authentication failed for token secret-value")}},
		errors:  []error{errors.New("exit status 255")},
	}
	client := newTestClient(t, runner)

	_, err := client.ListSnapshots(context.Background(), pluginapi.BackupSnapshotQuery{})
	var providerError *pluginapi.ProviderError
	if !errors.As(err, &providerError) || providerError.Code != pluginapi.ErrorAuthenticationFailed {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("provider error exposed raw stderr")
	}
}

func newTestClient(t *testing.T, runner CommandRunner) *Client {
	t.Helper()
	client, err := New(Config{
		ClientPath:    "proxmox-backup-client",
		Server:        "pbs.test.invalid",
		Port:          8007,
		Datastore:     "recovery",
		AuthID:        "drtui@pbs!dr",
		Namespace:     "dr",
		Fingerprint:   testFingerprint,
		Password:      pluginapi.SecretReference{Provider: "vault", Key: "pbs-token"},
		WorkDirectory: t.TempDir(),
	}, runner, staticSecrets{value: []byte("api-token-secret")})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func containsPair(values []string, key, value string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == key && values[index+1] == value {
			return true
		}
	}
	return false
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
