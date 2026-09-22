package pluginapi

import (
	"encoding/json"
	"net/netip"
	"time"
)

// RawConfig is schema-validated JSON passed to a provider. The engine converts
// YAML to JSON before crossing the plugin boundary; providers must reject
// unknown or invalid fields when decoding it into their private config type.
type RawConfig json.RawMessage

type PlanID string
type SecretKey string
type StorageURI string
type LocalPath string
type DomainName string
type ServiceID string
type BackupSnapshotID string
type BackupArchiveName string

type ProviderMetadata struct {
	Name            string
	Version         string
	ProtocolVersion uint32
	Capabilities    []string
}

type AuthConfig struct {
	Method     string
	Parameters RawConfig
}

// SecretReference identifies secret material without embedding it in a plan,
// output, execution journal, or plugin configuration.
type SecretReference struct {
	Provider string
	Key      SecretKey
}

type SSHConnection struct {
	Host           string
	Port           uint16
	User           string
	HostKeySHA256  string
	Credential     SecretReference
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
	Bastion        *BastionConnection
}

type BastionConnection struct {
	Host          string
	Port          uint16
	User          string
	HostKeySHA256 string
	Credential    SecretReference
}

type BackupMetadata struct {
	Format       string
	ArtifactPath LocalPath
	Properties   RawConfig
}

type BackupRepositoryStatus struct {
	Reachable      bool   `json:"reachable"`
	ServerVersion  string `json:"serverVersion,omitempty"`
	TotalBytes     uint64 `json:"totalBytes"`
	UsedBytes      uint64 `json:"usedBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

type BackupSnapshotQuery struct {
	Group     string `json:"group,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type BackupSnapshot struct {
	ID        BackupSnapshotID    `json:"id"`
	Group     string              `json:"group"`
	Timestamp time.Time           `json:"timestamp"`
	SizeBytes uint64              `json:"sizeBytes"`
	Archives  []BackupArchiveName `json:"archives"`
}

type BackupSnapshotReference struct {
	ID        BackupSnapshotID `json:"id"`
	Namespace string           `json:"namespace,omitempty"`
}

type BackupArchive struct {
	Name        BackupArchiveName `json:"name"`
	SizeBytes   uint64            `json:"sizeBytes"`
	CryptMode   string            `json:"cryptMode,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
}

type RestoreArchiveRequest struct {
	Snapshot         BackupSnapshotReference `json:"snapshot"`
	Archive          BackupArchiveName       `json:"archive"`
	LocalDestination LocalPath               `json:"localDestination"`
	Overwrite        bool                    `json:"overwrite,omitempty"`
}

type RestoreArchiveResult struct {
	Snapshot         BackupSnapshotID  `json:"snapshot"`
	Archive          BackupArchiveName `json:"archive"`
	LocalDestination LocalPath         `json:"localDestination"`
}

type NetworkMap struct {
	Routes []NetworkRoute
}

type NetworkRoute struct {
	Domain     DomainName
	Service    ServiceID
	TargetPort uint16
	Path       string
	Protocol   string
}

type ServiceDefinition struct {
	ID         ServiceID
	Kind       string
	APIVersion string
	Spec       RawConfig
}

type RuntimeConfig struct {
	Kind       string
	APIVersion string
	Spec       RawConfig
}

type ProvisionRequest struct {
	TargetName string
	Spec       RawConfig
}

type ProvisionResult struct {
	InstanceID string
	PublicIP   netip.Addr
	SSH        SSHConnection
}

type VerificationRequest struct {
	Target ProvisionResult
	Checks RawConfig
}

type VerificationResult struct {
	Healthy bool
	Details map[string]string
}
