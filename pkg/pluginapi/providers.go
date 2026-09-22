package pluginapi

import (
	"context"
	"net/netip"
)

// Provider is the lifecycle contract shared by in-process implementations and
// out-of-process RPC adapters.
type Provider interface {
	Metadata() ProviderMetadata
	Close(context.Context) error
}

type IKeyvaultProvider interface {
	Provider
	Initialize(ctx context.Context, authConfig AuthConfig) error
	FetchPlanSecrets(ctx context.Context, planID PlanID) (*SecretSet, error)
}

type IStorageProvider interface {
	Provider
	DownloadBlob(ctx context.Context, sourcePath StorageURI, localDestination LocalPath) error
}

// IBackupProvider models snapshot repositories whose contents cannot be treated
// as independent blobs. Proxmox Backup Server is one implementation; a virtual
// infrastructure provider can consume its snapshot references directly.
type IBackupProvider interface {
	Provider
	ProbeRepository(ctx context.Context) (BackupRepositoryStatus, error)
	ListSnapshots(ctx context.Context, query BackupSnapshotQuery) ([]BackupSnapshot, error)
	ListSnapshotFiles(ctx context.Context, snapshot BackupSnapshotReference) ([]BackupArchive, error)
	RestoreArchive(ctx context.Context, request RestoreArchiveRequest) (RestoreArchiveResult, error)
}

type IApplicationLayerProvider interface {
	Provider
	BootstrapRuntime(ctx context.Context, targetHost SSHConnection, config RuntimeConfig) (bool, error)
	ExtractNetworkTopology(ctx context.Context, backupMeta BackupMetadata) (NetworkMap, error)
	DeployService(ctx context.Context, serviceDefinition ServiceDefinition) error
}

type IDNSLayerProvider interface {
	Provider
	SyncRecords(ctx context.Context, domainList []DomainName, targetIP netip.Addr, proxyEnabled bool) error
}

// IDnsLayerProvider preserves the specification's spelling while
// IDNSLayerProvider follows Go's initialism convention.
type IDnsLayerProvider = IDNSLayerProvider

// IInfrastructureProvider is an optional capability for provisioning strategies
// such as Terraform. It keeps infrastructure lifecycle concerns out of the
// application runtime contract.
type IInfrastructureProvider interface {
	Provider
	Provision(ctx context.Context, request ProvisionRequest) (ProvisionResult, error)
	Verify(ctx context.Context, request VerificationRequest) (VerificationResult, error)
	Destroy(ctx context.Context, target ProvisionResult) error
}
