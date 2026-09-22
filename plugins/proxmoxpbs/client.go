package proxmoxpbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/drtui/drtui/pkg/pluginapi"
)

const defaultClientPath = "proxmox-backup-client"

var (
	authIDPattern      = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+(?:![A-Za-z0-9_.-]+)?$`)
	datastorePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	fingerprintPattern = regexp.MustCompile(`(?i)^(?:[0-9a-f]{2}:){31}[0-9a-f]{2}$`)
	groupPattern       = regexp.MustCompile(`^(?:vm|ct|host)/[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	namespacePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?:/[A-Za-z0-9][A-Za-z0-9_.-]*)*$`)
	snapshotPattern    = regexp.MustCompile(`^(?:vm|ct|host)/[A-Za-z0-9][A-Za-z0-9_.-]{0,127}/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	archivePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
)

type Config struct {
	ClientPath    string                    `json:"clientPath,omitempty"`
	Server        string                    `json:"server"`
	Port          uint16                    `json:"port,omitempty"`
	Datastore     string                    `json:"datastore"`
	AuthID        string                    `json:"authId"`
	Namespace     string                    `json:"namespace,omitempty"`
	Fingerprint   string                    `json:"fingerprint"`
	Password      pluginapi.SecretReference `json:"password"`
	WorkDirectory string                    `json:"workDirectory"`
}

type SecretResolver interface {
	Resolve(ctx context.Context, reference pluginapi.SecretReference) ([]byte, error)
}

type Client struct {
	config  Config
	runner  CommandRunner
	secrets SecretResolver
}

var _ pluginapi.IBackupProvider = (*Client)(nil)

func New(config Config, runner CommandRunner, secrets SecretResolver) (*Client, error) {
	if config.ClientPath == "" {
		config.ClientPath = defaultClientPath
	}
	if config.Port == 0 {
		config.Port = 8007
	}
	if runner == nil {
		runner = ExecRunner{MaxOutputBytes: 4 << 20}
	}
	if err := validateConfig(config); err != nil {
		return nil, &pluginapi.ProviderError{Code: pluginapi.ErrorInvalidConfig, Message: err.Error()}
	}
	if secrets == nil {
		return nil, &pluginapi.ProviderError{Code: pluginapi.ErrorInvalidConfig, Message: "secret resolver is required"}
	}
	workDirectory, err := filepath.Abs(config.WorkDirectory)
	if err != nil {
		return nil, &pluginapi.ProviderError{Code: pluginapi.ErrorInvalidConfig, Message: "resolve work directory", Cause: err}
	}
	config.WorkDirectory = filepath.Clean(workDirectory)
	return &Client{config: config, runner: runner, secrets: secrets}, nil
}

func (client *Client) Metadata() pluginapi.ProviderMetadata {
	return pluginapi.ProviderMetadata{
		Name:            "proxmox-backup-client",
		Version:         "0.1.0",
		ProtocolVersion: 1,
		Capabilities:    []string{"backup"},
	}
}

func (client *Client) Close(context.Context) error { return nil }

func (client *Client) ProbeRepository(ctx context.Context) (pluginapi.BackupRepositoryStatus, error) {
	versionJSON, err := client.runJSON(ctx, []string{"version", "--output-format", "json"}, false)
	if err != nil {
		return pluginapi.BackupRepositoryStatus{}, err
	}
	statusJSON, err := client.runJSON(ctx, []string{"status", "--output-format", "json"}, false)
	if err != nil {
		return pluginapi.BackupRepositoryStatus{}, err
	}

	version, err := decodeObject(versionJSON)
	if err != nil {
		return pluginapi.BackupRepositoryStatus{}, invalidOutput("version", err)
	}
	status, err := decodeObject(statusJSON)
	if err != nil {
		return pluginapi.BackupRepositoryStatus{}, invalidOutput("status", err)
	}
	return pluginapi.BackupRepositoryStatus{
		Reachable:      true,
		ServerVersion:  firstString(version, "server-version", "server_version", "server"),
		TotalBytes:     firstUint(status, "total", "total-bytes", "total_bytes"),
		UsedBytes:      firstUint(status, "used", "used-bytes", "used_bytes"),
		AvailableBytes: firstUint(status, "avail", "available", "available-bytes", "available_bytes"),
	}, nil
}

func (client *Client) ListSnapshots(ctx context.Context, query pluginapi.BackupSnapshotQuery) ([]pluginapi.BackupSnapshot, error) {
	if query.Group != "" && !groupPattern.MatchString(query.Group) {
		return nil, invalidRequest("invalid backup group")
	}
	if err := validateNamespace(query.Namespace); err != nil {
		return nil, invalidRequest(err.Error())
	}
	args := []string{"snapshot", "list"}
	if query.Group != "" {
		args = append(args, query.Group)
	}
	args = append(args, "--output-format", "json")
	output, err := client.runJSONWithNamespace(ctx, args, query.Namespace)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(output)
}

func (client *Client) ListSnapshotFiles(ctx context.Context, snapshot pluginapi.BackupSnapshotReference) ([]pluginapi.BackupArchive, error) {
	if err := validateSnapshotReference(snapshot); err != nil {
		return nil, invalidRequest(err.Error())
	}
	output, err := client.runJSONWithNamespace(ctx, []string{
		"snapshot", "files", string(snapshot.ID), "--output-format", "json",
	}, snapshot.Namespace)
	if err != nil {
		return nil, err
	}
	return parseArchives(output)
}

func (client *Client) RestoreArchive(ctx context.Context, request pluginapi.RestoreArchiveRequest) (pluginapi.RestoreArchiveResult, error) {
	if err := validateSnapshotReference(request.Snapshot); err != nil {
		return pluginapi.RestoreArchiveResult{}, invalidRequest(err.Error())
	}
	if !archivePattern.MatchString(string(request.Archive)) {
		return pluginapi.RestoreArchiveResult{}, invalidRequest("invalid archive name")
	}
	destination, err := client.resolveDestination(request.LocalDestination)
	if err != nil {
		return pluginapi.RestoreArchiveResult{}, invalidRequest(err.Error())
	}
	args := []string{"restore", string(request.Snapshot.ID), string(request.Archive), destination}
	if request.Overwrite {
		args = append(args, "--overwrite", "true")
	}
	if _, err := client.run(ctx, args, request.Snapshot.Namespace); err != nil {
		return pluginapi.RestoreArchiveResult{}, err
	}
	return pluginapi.RestoreArchiveResult{
		Snapshot:         request.Snapshot.ID,
		Archive:          request.Archive,
		LocalDestination: pluginapi.LocalPath(destination),
	}, nil
}

func (client *Client) runJSON(ctx context.Context, args []string, includeNamespace bool) ([]byte, error) {
	namespace := ""
	if includeNamespace {
		namespace = client.config.Namespace
	}
	return client.run(ctx, args, namespace)
}

func (client *Client) runJSONWithNamespace(ctx context.Context, args []string, namespace string) ([]byte, error) {
	if namespace == "" {
		namespace = client.config.Namespace
	}
	return client.run(ctx, args, namespace)
}

func (client *Client) run(ctx context.Context, args []string, namespace string) ([]byte, error) {
	password, err := client.secrets.Resolve(ctx, client.config.Password)
	if err != nil {
		return nil, &pluginapi.ProviderError{Code: pluginapi.ErrorAuthenticationFailed, Message: "resolve PBS credential", Cause: err}
	}
	defer clear(password)
	if len(password) == 0 || bytes.IndexByte(password, '\n') >= 0 || bytes.IndexByte(password, '\r') >= 0 {
		return nil, &pluginapi.ProviderError{Code: pluginapi.ErrorAuthenticationFailed, Message: "PBS credential is empty or contains a newline"}
	}

	commandArgs := append([]string(nil), args...)
	commandArgs = append(commandArgs,
		"--server", client.config.Server,
		"--port", strconv.FormatUint(uint64(client.config.Port), 10),
		"--datastore", client.config.Datastore,
		"--auth-id", client.config.AuthID,
	)
	if namespace != "" {
		commandArgs = append(commandArgs, "--ns", namespace)
	}
	stdin := append([]byte(nil), password...)
	stdin = append(stdin, '\n')
	defer clear(stdin)

	result, runErr := client.runner.Run(ctx, Command{
		Path: client.config.ClientPath,
		Args: commandArgs,
		Env: map[string]string{
			"PBS_FINGERPRINT":          client.config.Fingerprint,
			"PBS_LOG":                  "error",
			"PBS_PASSWORD_FD":          "0",
			"PROXMOX_OUTPUT_FORMAT":    "json",
			"PROXMOX_OUTPUT_NO_BORDER": "1",
			"PROXMOX_OUTPUT_NO_HEADER": "1",
		},
		Stdin: stdin,
	})
	if runErr != nil {
		return nil, classifyCommandError(ctx, result, runErr)
	}
	return result.Stdout, nil
}

func (client *Client) resolveDestination(destination pluginapi.LocalPath) (string, error) {
	if destination == "" || destination == "-" {
		return "", errors.New("restore destination must be a filesystem path")
	}
	target := string(destination)
	if !filepath.IsAbs(target) {
		target = filepath.Join(client.config.WorkDirectory, target)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve restore destination: %w", err)
	}
	relative, err := filepath.Rel(client.config.WorkDirectory, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("restore destination escapes the provider work directory")
	}
	return filepath.Clean(target), nil
}

func validateConfig(config Config) error {
	if config.ClientPath == "" {
		return errors.New("client path is required")
	}
	if config.Server == "" || strings.HasPrefix(config.Server, "-") || strings.ContainsAny(config.Server, "\r\n\x00") {
		return errors.New("valid PBS server is required")
	}
	if net.ParseIP(strings.Trim(config.Server, "[]")) == nil && strings.ContainsAny(config.Server, " /\\") {
		return errors.New("PBS server must be a hostname or IP address")
	}
	if !datastorePattern.MatchString(config.Datastore) {
		return errors.New("invalid PBS datastore")
	}
	if !authIDPattern.MatchString(config.AuthID) {
		return errors.New("invalid PBS authentication ID")
	}
	if !fingerprintPattern.MatchString(config.Fingerprint) {
		return errors.New("PBS TLS fingerprint must contain 32 colon-separated SHA-256 octets")
	}
	if err := validateNamespace(config.Namespace); err != nil {
		return err
	}
	if config.Password.Provider == "" || config.Password.Key == "" {
		return errors.New("PBS password secret reference is required")
	}
	if config.WorkDirectory == "" {
		return errors.New("work directory is required")
	}
	return nil
}

func validateNamespace(namespace string) error {
	if namespace != "" && !namespacePattern.MatchString(namespace) {
		return errors.New("invalid PBS namespace")
	}
	return nil
}

func validateSnapshotReference(snapshot pluginapi.BackupSnapshotReference) error {
	if !snapshotPattern.MatchString(string(snapshot.ID)) {
		return errors.New("invalid PBS snapshot ID")
	}
	return validateNamespace(snapshot.Namespace)
}

func parseSnapshots(data []byte) ([]pluginapi.BackupSnapshot, error) {
	rows, err := decodeRows(data)
	if err != nil {
		return nil, invalidOutput("snapshot list", err)
	}
	snapshots := make([]pluginapi.BackupSnapshot, 0, len(rows))
	for _, row := range rows {
		id := firstString(row, "snapshot")
		if id == "" {
			backupType := firstString(row, "backup-type", "backup_type")
			backupID := firstString(row, "backup-id", "backup_id")
			backupTime := firstUint(row, "backup-time", "backup_time")
			if backupType != "" && backupID != "" && backupTime != 0 {
				id = fmt.Sprintf("%s/%s/%s", backupType, backupID, time.Unix(int64(backupTime), 0).UTC().Format(time.RFC3339))
			}
		}
		if !snapshotPattern.MatchString(id) {
			return nil, invalidOutput("snapshot list", fmt.Errorf("invalid snapshot ID %q", id))
		}
		timestamp, err := time.Parse(time.RFC3339, id[strings.LastIndexByte(id, '/')+1:])
		if err != nil {
			return nil, invalidOutput("snapshot list", err)
		}
		archives, err := stringList(row["files"])
		if err != nil {
			return nil, invalidOutput("snapshot list", err)
		}
		typedArchives := make([]pluginapi.BackupArchiveName, len(archives))
		for index, archive := range archives {
			typedArchives[index] = pluginapi.BackupArchiveName(archive)
		}
		snapshots = append(snapshots, pluginapi.BackupSnapshot{
			ID:        pluginapi.BackupSnapshotID(id),
			Group:     id[:strings.LastIndexByte(id, '/')],
			Timestamp: timestamp,
			SizeBytes: firstUint(row, "size"),
			Archives:  typedArchives,
		})
	}
	return snapshots, nil
}

func parseArchives(data []byte) ([]pluginapi.BackupArchive, error) {
	rows, err := decodeRows(data)
	if err != nil {
		return nil, invalidOutput("snapshot files", err)
	}
	archives := make([]pluginapi.BackupArchive, 0, len(rows))
	for _, row := range rows {
		name := firstString(row, "filename", "name")
		if !archivePattern.MatchString(name) {
			return nil, invalidOutput("snapshot files", fmt.Errorf("invalid archive name %q", name))
		}
		archives = append(archives, pluginapi.BackupArchive{
			Name:        pluginapi.BackupArchiveName(name),
			SizeBytes:   firstUint(row, "size"),
			CryptMode:   firstString(row, "crypt-mode", "crypt_mode"),
			Fingerprint: firstString(row, "fingerprint"),
		})
	}
	return archives, nil
}

func decodeRows(data []byte) ([]map[string]any, error) {
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func decodeObject(data []byte) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	return ""
}

func firstUint(object map[string]any, keys ...string) uint64 {
	for _, key := range keys {
		switch value := object[key].(type) {
		case json.Number:
			parsed, _ := strconv.ParseUint(string(value), 10, 64)
			return parsed
		case float64:
			if value >= 0 {
				return uint64(value)
			}
		}
	}
	return 0
}

func stringList(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return strings.Fields(typed), nil
	case []any:
		values := make([]string, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, errors.New("archive list contains a non-string value")
			}
			values[index] = text
		}
		return values, nil
	default:
		return nil, errors.New("archive list has an unsupported shape")
	}
}

func invalidRequest(message string) error {
	return &pluginapi.ProviderError{Code: pluginapi.ErrorInvalidConfig, Message: message}
}

func invalidOutput(action string, err error) error {
	return &pluginapi.ProviderError{Code: pluginapi.ErrorInternal, Message: action + " returned invalid JSON", Cause: err}
}

func classifyCommandError(ctx context.Context, result CommandResult, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &pluginapi.ProviderError{Code: pluginapi.ErrorTimeout, Message: "PBS client deadline exceeded", Retryable: true, Cause: err}
	}
	message := strings.ToLower(string(result.Stderr))
	switch {
	case strings.Contains(message, "authentication"), strings.Contains(message, "permission denied"), strings.Contains(message, "unauthorized"):
		return &pluginapi.ProviderError{Code: pluginapi.ErrorAuthenticationFailed, Message: "PBS authentication failed", Cause: err}
	case strings.Contains(message, "not found"), strings.Contains(message, "does not exist"):
		return &pluginapi.ProviderError{Code: pluginapi.ErrorNotFound, Message: "PBS resource not found", Cause: err}
	case strings.Contains(message, "connection"), strings.Contains(message, "timed out"), strings.Contains(message, "temporarily unavailable"):
		return &pluginapi.ProviderError{Code: pluginapi.ErrorTransient, Message: "PBS repository is unavailable", Retryable: true, Cause: err}
	default:
		return &pluginapi.ProviderError{Code: pluginapi.ErrorInternal, Message: "PBS client command failed", Cause: err}
	}
}
