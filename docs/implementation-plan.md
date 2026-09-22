# drtui implementation plan

## Outcome

Deliver a stateless CLI that can validate, package, inspect, and execute the
Coolify-to-S3-to-Cloudflare recovery plan using digest-pinned, out-of-process
providers. Every component must be testable without live cloud credentials, and
the complete workflow must be rehearsable against a disposable Linux server and
a non-production DNS zone.

The work should land as small vertical changes. A phase is complete only when
its tests and command-level acceptance criteria pass; later phases must not be
used to compensate for an untestable earlier layer.

## Decisions to freeze first

1. Provider plugins are separate executables speaking versioned gRPC. Native Go
   shared-object plugins are not supported.
2. A provider `digest` is the OCI manifest digest. A lockfile additionally pins
   the platform-specific binary blob digest and embedded plugin manifest digest.
3. Local development may use `file://` plugins, but their SHA-256 is still
   required and verified before launch.
4. The CLI owns plan compilation, scheduling, retries, journals, secret leases,
   package verification, and built-in filesystem cleanup. Providers own only
   their external-system behavior.
5. Tests use three destination tiers:
   - An in-process SSH server for default, network-local tests.
   - A Docker Linux SSH server for transport and remote-operation integration.
   - A disposable AWS EC2 Ubuntu host for the full Coolify rehearsal.
6. The production example remains signature-required. Test plans live under
   `testdata` and refer to locally built plugins with generated real digests.
7. No test changes a production DNS record or reads production backup objects.

AWS is the default cloud rehearsal target because the reference plan already
uses S3 and AWS Secrets Manager. The infrastructure contract remains cloud
neutral, so another destination can replace EC2 later.

## Intended repository layout

```text
cmd/
|-- drtui/                        Main CLI
|-- drtui-plugin-aws-secrets/     AWS Secrets Manager provider
|-- drtui-plugin-s3/              S3/R2/Garage storage provider
|-- drtui-plugin-proxmox-pbs/     Proxmox Backup Server CLI provider
|-- drtui-plugin-terraform/       Terraform infrastructure provider
|-- drtui-plugin-proxmox-ve/      Proxmox VE VM/container provider
|-- drtui-plugin-coolify/         Coolify application provider
`-- drtui-plugin-cloudflare/      Cloudflare DNS provider
api/proto/provider/v1/             Protocol source
gen/provider/v1/                   Checked-in generated Go protocol
internal/
|-- app/                           CLI use cases and dependency wiring
|-- expression/                    Parser, resolver, and type checker
|-- engine/                        Scheduler, retries, rollback, state
|-- journal/                       Redacted append-only JSONL journal
|-- package/                       .drp archive, lockfile, manifest, signatures
|-- plan/                          YAML load, schema, semantic compilation
|-- pluginhost/                    Discovery, digest check, process, gRPC adapters
|-- remote/                        SSH probe and reusable remote primitives
|-- secretbroker/                  Scoped, expiring secret leases for plugins
`-- testkit/                       Clocks, fake providers, fixtures, fault scripts
plugins/
|-- awssecrets/
|-- s3/
|-- proxmoxpbs/
|-- terraform/
|-- proxmoxve/
|-- coolify/
`-- cloudflare/
pkg/
|-- pluginapi/                     Stable in-process contracts
|-- pluginsdk/                     Plugin server, errors, schema helpers
`-- plugintest/                    Public provider conformance suite
test/
|-- integration/                  Process, Docker, and emulator tests
|-- system/                       Credentialed disposable-cloud drills
`-- fixtures/
    |-- plugins/                   Deterministic test plugin
    |-- sshd/                      Linux SSH destination image
    |-- plans/                     Minimal plans per engine behavior
    `-- backups/                   Synthetic Coolify and PostgreSQL data
tools/                             Pinned Buf, lint, signing, and release tooling
```

## Test modes and isolation

| Level | Command | Network | Purpose |
| --- | --- | --- | --- |
| Unit | `go test ./...` | No external network | Parsers, types, DAG, retries, redaction, provider logic with fake clients |
| Integration | `go test ./test/integration/... -count=1` | Loopback/Docker | Plugin processes, gRPC, LocalStack/MinIO, fake Cloudflare API, Linux SSH target |
| System | `go test ./test/system/... -count=1 -timeout 90m` | AWS/test DNS zone | Provision VM, install Coolify, restore synthetic data, cut over test DNS, rollback |
| Compatibility | `go test ./pkg/plugintest/...` | Plugin-defined | Contract suite external plugin authors can import |

Default tests must pass when Docker, cloud credentials, Terraform, and `protoc`
are absent. Integration tests detect an unavailable Docker daemon and skip with
one actionable reason. CI runs integration tests on Linux where Docker is
guaranteed. System tests run only through a manual or scheduled workflow.

### Isolated plan execution

Implement these commands before live providers:

```text
drtui plan validate <plan-or-package>
drtui plan graph <plan-or-package> --format text|json|dot
drtui run <package> --dry-run
drtui run <package> --to <step-id>
drtui test step <plan> --step <step-id> --context <fixture.json>
drtui target probe --connection <connection.json> --identity-file <path>
```

`run --to` executes the selected step's complete dependency closure and then
stops. It is the supported way to rehearse a real prefix such as provisioning
through SSH verification or downloads through checksum validation.

`test step` is deliberately separate from `run`. It invokes exactly one action
with schema-validated fixture inputs and synthetic predecessor outputs. It may
use only `file://` plugins, rejects production-signature policy, marks every
journal event as test-only, and cannot package its result as a resumable run.
This prevents fixture context from bypassing dependencies in production.

Each provider action gets a contract fixture with:

- A valid request and expected typed response.
- Invalid configuration and invalid request cases.
- Cancellation and deadline behavior.
- A retryable failure followed by success using the same idempotency key.
- A repeated successful invocation proving idempotency.
- Secret-input and log-redaction assertions.
- A rollback or cleanup assertion for every mutating action.

## Destination server strategy

### Tier 1: in-process SSH fixture

Create an SSH server in `internal/testkit/sshserver` using `x/crypto/ssh`. It
generates an Ed25519 host key and client key per test, binds to `127.0.0.1:0`,
and exposes the exact SHA-256 host-key fingerprint. It supports only the minimal
exec and file-transfer operations needed by the probe tests.

This fixture validates authentication, host-key pinning, context cancellation,
timeouts, output limits, and secret cleanup on every platform. It is not used to
claim Linux or systemd compatibility.

### Tier 2: Docker Linux destination

Build `test/fixtures/sshd/Dockerfile` from a pinned Ubuntu 24.04 digest. The
fixture creates a non-root `drtui` user, OpenSSH, a writable staging path,
and deterministic test commands. Testcontainers launches it on a random local
port and generates credentials at runtime; no fixed private key is committed.

The connectivity probe must perform, in order:

1. Validate host, port, user, credential reference, and pinned fingerprint.
2. Resolve the host and open TCP within the connect deadline.
3. Complete an SSH handshake and reject any fingerprint mismatch.
4. Authenticate with the short-lived Ed25519 key.
5. Execute a no-shell probe that returns OS, architecture, free disk, and UTC.
6. Upload, hash, download, compare, and delete a random file through SFTP.
7. Optionally verify declared egress endpoints without sending credentials.
8. Return a typed `ConnectivityReport`; redact usernames and paths only where
   policy marks them sensitive, and always redact credentials.

Required negative tests cover wrong host key, wrong private key, unreachable
port, command timeout, oversized output, interrupted upload, and cleanup after
cancellation. The current workstation has the Docker CLI but its daemon is not
reachable, so enabling Docker Desktop is a prerequisite for this tier locally.

### Tier 3: disposable AWS destination

Create a test-only Terraform module that provisions one Ubuntu 24.04 EC2
instance, an encrypted root volume, and a security group restricted to the
runner's current public IP. Tag every resource with `drtui-test`, run ID,
owner, and an expiry timestamp. A scheduled janitor deletes expired resources.

Generate a client key in memory, register only its public key, and publish the
SSH host-key fingerprint through the authenticated AWS control plane using SSM
before direct SSH begins. Do not use `StrictHostKeyChecking=no`, trust-on-first-
use, embedded keys, or a private key persisted in Terraform state.

The first cloud system test executes only through `verify_target` and proves:

- Terraform provision output decodes into `ProvisionResult`.
- The returned public IP is reachable on the restricted SSH port.
- The host fingerprint obtained through AWS matches the SSH handshake.
- Authentication, command execution, SFTP round trip, disk check, and HTTPS
  egress succeed.
- Rollback destroys the instance, volume, key registration, and security group.

The full system test later reuses this destination for Coolify. It must run in a
dedicated AWS account or sandbox OU with a budget alarm and no production VPC
peering.

## Phased implementation

### Phase 0: protocol and build foundation

Deliverables:

- Pin Buf and protobuf Go generators; add `buf lint`, generation, and breaking
  change checks. Check generated Go into `gen/provider/v1`.
- Extend the protocol with structured error details and a host-side secret lease
  broker. Plugins receive opaque, action-scoped handles and resolve them over
  authenticated inherited IPC; raw secrets never enter JSON action payloads.
- Define `plugins.lock.json`, package manifest, platform tuple, and OCI artifact
  schemas. Include protocol version and schema digest in every lock entry.
- Add canonical JSON and SHA-256 helpers with cross-platform golden vectors.
- Build a deterministic test plugin implementing every capability and scripted
  success, delay, crash, malformed-output, and retryable-error modes.

Tests and gate:

- Protocol lint and generated-code cleanliness pass in CI.
- Go and one non-Go fixture can complete handshake and one extension action.
- Lockfile and artifact digest golden tests pass on Windows and Linux.
- Secret handles reject the wrong process, action, invocation, and expired lease.

### Phase 1: plan loader and compiler

Deliverables:

- Strict YAML 1.2 loader with aliases disabled, size/depth limits, duplicate-key
  rejection, canonical JSON conversion, and JSON Schema validation.
- Provider metadata loading followed by provider config/action schema checks.
- Typed expression parser and resolver; no regex-based evaluation.
- Semantic checks for unique IDs, transaction references, transitive output
  dependencies, secret-flow restrictions, durations, and `forEach` item types.
- Stable DAG expansion, declaration-order tie breaking, and plan digest.
- `plan validate`, `plan graph`, and `run --dry-run` commands.

Tests and gate:

- Table tests and fuzzers cover YAML, expressions, dependency edges, and types.
- Golden output proves identical graph and digest across repeated runs.
- Every expression and action in the reference plan compiles against fixture
  provider manifests without invoking a provider action.

### Phase 2: execution engine and journal

Deliverables:

- Bounded scheduler, dependency blocking, conditions, deterministic `forEach`,
  context cancellation, fixed exponential retry, and per-step deadlines.
- Immutable state publication with runtime validation of advertised outputs.
- Compensating transactions with rollback-input snapshots and reverse
  topological rollback.
- Built-in `engine/removePath` constrained to the run work directory.
- Append-only JSONL journal with sequence numbers, redaction provenance, output
  hashes, attempt metadata, and a terminal run summary.
- `run --to` and the isolated `test step` harness.

Tests and gate:

- A fake clock makes retries and deadlines fast and deterministic.
- Fault-injection tests cover every state transition and rollback interruption.
- Race tests pass under `go test -race ./...` on Linux.
- Re-running fixture actions produces the same idempotency keys and journal
  ordering, excluding explicitly documented timestamps.

### Phase 3: plugin host and provider conformance kit

Deliverables:

- Verify the binary digest before execution, create an authenticated loopback or
  inherited-pipe channel, perform handshake, and negotiate protocol version.
- Implement lifecycle and capability-specific gRPC adapters plus extension
  action dispatch.
- Enforce process deadlines, output limits, graceful close, forced termination,
  and temporary-directory cleanup.
- Map gRPC statuses and provider details to `ProviderError` without leaking raw
  process output.
- Publish `pkg/pluginsdk` and `pkg/plugintest` for third-party providers.

Tests and gate:

- The scripted test plugin passes the full conformance suite as a child process.
- Digest mismatch prevents process launch.
- Crash, hang, bad handshake, incompatible protocol, malformed schema, invalid
  output, and secret-log attempts fail with stable redacted errors.

### Phase 4: remote transport and destination probe

Deliverables:

- Implement `internal/remote` with host-key-pinned SSH, SFTP, deadlines, bounded
  output, argument validation, and no ambient SSH agent fallback.
- Add `ConnectivityReport` and `drtui target probe`.
- Add Tier 1 and Tier 2 destination fixtures and all negative cases above.
- Add a built-in `engine/probeSSH` action and place it immediately after
  `provision_target` in the reference plan. Keep provider-specific prerequisite
  verification as a separate step.

Tests and gate:

- Unit tests pass on Windows without Docker.
- Docker integration proves a real Linux SSH and SFTP round trip.
- A wrong host-key fingerprint is always fatal and never retryable.

### Phase 5: providers, one vertical slice at a time

#### AWS Secrets Manager

Use AWS SDK for Go v2. `initialize` loads the selected credential chain and calls
STS `GetCallerIdentity`. `fetchPlanSecrets` reads one configured secret document
at `drtui/plans/<plan-id>`, requires a JSON string map, and returns leased
mutable values.

Advertised actions: `initialize`, `fetchPlanSecrets`.

Isolated tests use fake STS/Secrets Manager clients; integration uses LocalStack.
Acceptance includes missing keys, malformed JSON, denied access, expiry,
cancellation, and zero secret bytes in logs or journals.

#### S3-compatible storage

Use AWS SDK for Go v2 with optional endpoint, region, path-style, and TLS policy
for S3, R2, Garage, and MinIO. Parse storage URIs structurally. Download to a
same-directory temporary file, stream with limits, verify a required checksum,
`fsync`, atomically rename, and remove partial files on every failure.

Advertised action: `downloadBlob`.

Unit tests use a fake client; integration uses MinIO or LocalStack. Acceptance
includes ranged/retried downloads, checksum mismatch, cancellation, overwrite
policy, and work-directory escape rejection.

#### Terraform infrastructure

Wrap a pinned Terraform CLI through `exec.CommandContext`; never scrape human
output. Use `terraform show/output -json`, a constrained module directory, an
explicit backend, and a per-run data directory. Normalize diagnostics into
structured errors. Preserve transient state through rollback in the same run;
cloud rehearsals use a dedicated encrypted remote backend so cleanup can recover
after runner failure.

Advertised actions: `provision`, `verify`, `destroy`.

Unit tests use a scripted fake executable. Integration uses a local-only test
module. The first AWS system slice provisions the Tier 3 destination, runs the
probe, injects a later failure, and proves destroy removes every resource.

#### Proxmox Backup Server

Wrap the official `proxmox-backup-client` CLI behind `IBackupProvider`. Keep PBS
separate from Proxmox VE: PBS owns repository reachability, snapshot inventory,
archive inventory, and archive restore; it does not allocate or start guests.

Advertised actions: `probeRepository`, `listSnapshots`, `listSnapshotFiles`,
`restoreArchive`.

Always use component flags (`--server`, `--port`, `--datastore`, `--auth-id`),
JSON output, `PBS_PASSWORD_FD`, and a pinned `PBS_FINGERPRINT`. Reject restore
paths outside the engine work directory and never automatically retry a missing
or invalid archive signature with `--ignore-missing-signature`.

The initial implementation lives in `plugins/proxmoxpbs` with an injected
command runner. Unit tests use scripted JSON and failures, so they run without a
PBS installation. Linux integration installs a pinned client and connects to a
disposable PBS test datastore containing host, VM, and container snapshots.
Acceptance covers token authentication, wrong TLS fingerprint, namespaces,
snapshot parsing, archive inventory, cancellation, bounded output, partial
restore cleanup, encrypted-archive fixtures, and no secret material in process
arguments, environment, errors, or journals.

#### Proxmox VE infrastructure

Implement Proxmox VE as a separate `IInfrastructureProvider`, using its HTTPS
API for normal lifecycle operations and narrowly scoped CLI execution only where
the API cannot express a restore. Extend `ProvisionRequest.Spec` with typed
guest modes: create from template, clone, restore VM from PBS, and restore
container from PBS. A PBS restore request carries the exact snapshot ID,
namespace, target storage, new VMID, and overwrite policy; selecting `latest`
implicitly is forbidden in production plans.

Advertised core actions: `provision`, `verify`, `destroy`. Advertised extension
actions: `restoreVM`, `restoreContainer`, `startGuest`, `stopGuest`.

Unit tests use fake PVE API and CLI clients. System tests require a dedicated
nested-virtualization Proxmox lab, restore a tiny synthetic guest from PBS,
obtain its guest-agent or DHCP address, run the destination SSH probe, inject a
failure, and prove guest plus temporary storage configuration are removed.
These tests are manual/scheduled and never target a production cluster.

#### Coolify application

Split SSH transport, Coolify API, backup parsing, and command planning behind
small interfaces. Pin the supported Coolify release and installer artifact by
checksum; do not pipe a remote script into a shell. Implement and test these
advertised actions independently:

```text
bootstrapRuntime              destroyRuntime
injectEnvironment             removeEnvironmentOverride
deployService                 removeService
pingDatabase
extractNetworkTopology
extractServiceDefinitions
verifyRoutes
```

Pure tests use synthetic backup metadata and golden service/network maps.
Transport tests use the Docker destination with a fake systemd/Coolify command
adapter. The EC2 system test uses real systemd, Docker, Coolify, PostgreSQL, and
a tiny synthetic application. Every mutating action must have an idempotency and
compensation test before it enters the full plan.

#### Cloudflare DNS

Inject an HTTP client and API base URL. Resolve zones exactly, list managed
record types, perform idempotent batch upserts, snapshot prior records, and
verify through both the Cloudflare API and authoritative DNS. Never infer a zone
from an unvalidated public suffix.

Advertised actions: `syncRecords`, `verifyRecords`.

Unit tests use `httptest.Server`. The live system test uses only a delegated
ephemeral subdomain in a dedicated test zone and always restores or deletes its
records. Test partial batch failure, rate limiting, proxy state, TTL, rollback,
and eventual DNS convergence with a bounded deadline.

### Phase 6: package, lock, signatures, and real digests

Deliverables:

- Implement safe `.drp` ZIP loading and creation, manifest verification, archive
  limits, path normalization, and immutable extraction.
- Implement OCI pull by digest and a content-addressed local plugin cache.
- Add `plugin build`, `plugin inspect`, `plugin lock`, and `plugin verify`.
- Produce reproducible binaries for Windows and Linux on amd64 and arm64.
- Package each plugin manifest plus platform blobs as an OCI artifact, publish to
  GHCR, and sign the manifest digest with Sigstore/Cosign identity policy.
- Generate the example lockfile and replace placeholder plan digests from the
  published OCI manifests. Never hand-edit these values.

Tests and gate:

- Archive traversal, symlink, duplicate-name, decompression-bomb, tamper, wrong
  platform, wrong digest, and invalid-signature tests all fail closed.
- Two clean builds of the same source and toolchain produce identical blobs.
- The packaged reference plan validates and dry-runs with network access removed
  after its plugins are cached.

### Phase 7: complete recovery rehearsal

Create synthetic fixtures: a small PostgreSQL schema with a known migration, a
Coolify metadata backup defining one static application, two test hostnames, and
versioned S3 objects with checksums. Store credentials in a sandbox Secrets
Manager path and use a delegated Cloudflare test subdomain.

Run the plan in three checkpoints:

1. `--to verify_target`: provision EC2, pin and test SSH, then roll it back.
2. `--to verify_applications`: restore Coolify and PostgreSQL, verify schema and
   local routes, then roll everything back without touching DNS.
3. Full run: update test DNS, verify proxied health, inject a post-DNS failure,
   prove reverse-order compensation restores DNS and destroys the host, then run
   once without injection and clean up explicitly.

The rehearsal passes only when the journal contains no secret material, every
resource is deleted, the test DNS zone is restored, and a second identical run
demonstrates provider idempotency.

## CI and release gates

Pull requests run formatting, vet, unit tests, race tests on Linux, schema tests,
Buf lint/breaking checks, secret scanning, and Docker integration. Windows runs
unit tests and the in-process plugin/SSH suites to protect cross-platform support.

Nightly or manually approved workflows use GitHub OIDC for short-lived AWS and
Cloudflare credentials. They enforce concurrency limits, cost ceilings, a hard
timeout, unconditional cleanup, and an independent expiry janitor. Provider OCI
artifacts publish only after unit, conformance, integration, vulnerability, and
provenance checks pass.

## Definition of done

- All 24 action uses in the reference plan resolve to a built-in or signed,
  digest-pinned provider action with validated input/output schemas.
- The default test suite requires no external service and tests every engine
  state transition and provider action.
- A real Linux destination can be probed independently with mandatory host-key
  verification before any runtime mutation.
- Prefix runs and isolated step tests work without weakening production DAG or
  signature rules.
- The disposable-cloud drill restores synthetic data, verifies the database and
  application, changes only test DNS, proves compensation, and leaves no cloud
  resources behind.
- The committed production example contains generated real OCI digests and a
  lockfile whose signatures verify from a clean machine.

## Recommended implementation order

Start with Phases 0 through 4 in order. After the conformance kit and destination
probe are stable, AWS Secrets Manager and S3 can proceed in parallel; Terraform
must complete before the EC2 destination test; Coolify follows the real SSH
probe; Cloudflare can proceed independently against its fake API. Package and
digest publishing starts once one provider passes conformance, then becomes the
release path for all remaining providers.