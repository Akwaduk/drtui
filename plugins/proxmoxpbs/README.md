# Proxmox Backup Server provider

This package implements `pluginapi.IBackupProvider` by invoking the official
`proxmox-backup-client` executable. It currently supports:

- `probeRepository`
- `listSnapshots`
- `listSnapshotFiles`
- `restoreArchive`

It is the provider core and manifest. The out-of-process gRPC executable will be
added when the shared plugin SDK from Phase 0 is available.

## Security properties

- Commands are executed directly, never through a shell.
- The API token is read by the child through `PBS_PASSWORD_FD=0`; it is absent
  from arguments and environment values.
- `PBS_FINGERPRINT` is mandatory and validates the PBS TLS certificate.
- Ambient `PBS_*` and `PROXMOX_OUTPUT_*` variables are removed before launch.
- Output is bounded and raw stderr is never included in a provider error.
- Restore destinations must remain below the configured work directory.
- `--ignore-missing-signature` is never added automatically.

## Isolated tests

The normal suite uses a scripted command runner and does not require Linux, PBS,
or `proxmox-backup-client`:

```powershell
go test ./plugins/proxmoxpbs -v
```

To probe a live test repository, install `proxmox-backup-client` and set:

```powershell
$env:DRTUI_PBS_TEST = "1"
$env:DRTUI_PBS_SERVER = "pbs.lab.example"
$env:DRTUI_PBS_PORT = "8007"
$env:DRTUI_PBS_DATASTORE = "recovery"
$env:DRTUI_PBS_AUTH_ID = "drtui@pbs!dr"
$env:DRTUI_PBS_TOKEN = "<test-token>"
$env:DRTUI_PBS_FINGERPRINT = "AA:BB:...:FF"
$env:DRTUI_PBS_NAMESPACE = "dr"
$env:DRTUI_PBS_GROUP = "vm/101" # optional
go test ./plugins/proxmoxpbs -run TestIntegrationPBSRepository -v -count=1
```

Use a read-only API token scoped to the test datastore. The test probes status
and lists snapshots; it does not restore, modify, prune, or delete data.

## Plan composition

An archive can feed a later application or infrastructure step through an
engine-owned path:

```yaml
providers:
  pbs:
    kind: backup
    source: oci://ghcr.io/drtui/proxmox-backup-provider
    version: 0.1.0
    digest: sha256:<generated-manifest-digest>
    config:
      server: pbs.lab.example
      port: 8007
      datastore: recovery
      authId: drtui@pbs!dr
      namespace: dr
      fingerprint: AA:BB:...:FF
      password:
        provider: recovery_secrets
        key: PBS_API_TOKEN
      workDirectory: "${{ runtime.workDir.pbs }}"

steps:
  - id: probe_pbs
    name: Verify the backup repository
    uses: pbs/probeRepository
    timeout: 30s

  - id: restore_config
    name: Restore a configuration archive
    uses: pbs/restoreArchive
    needs: [probe_pbs]
    timeout: 20m
    with:
      snapshot:
        id: "${{ inputs.pbs_snapshot }}"
        namespace: dr
      archive: etc.pxar
      localDestination: "${{ runtime.workDir.pbs-etc }}"
      overwrite: false
```

For a complete guest restore, do not download a VM image merely to upload it
again. Compose the future Proxmox VE infrastructure provider with the exact PBS
snapshot reference:

```yaml
- id: restore_vm
  name: Restore VM 101 onto the recovery cluster
  uses: proxmox/restoreVM
  needs: [probe_pbs]
  timeout: 45m
  with:
    backupProvider: pbs
    snapshot:
      id: "${{ inputs.pbs_snapshot }}"
      namespace: dr
    target:
      node: pve-recovery-01
      vmId: 9101
      storage: local-zfs
```

The Proxmox VE provider owns VM allocation, `qmrestore` or API task tracking,
guest startup, address discovery, and rollback. This provider owns only PBS
repository and archive behavior.