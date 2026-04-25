# Design: Why etcd-steward Replaces etcd-backup-restore

## Status

Draft -- 2026-04-24

## Authors

- @unmarshall (Amshuman K.)
- @seshachalam-yv (Seshachalam Y.V.)

## Related Documents

- [DEP-04: EtcdMember Custom Resource](../proposals/04-etcd-member-custom-resource.md)
- [Proposal: etcd-steward Rollout Plan](../proposals/08-etcd-steward-rollout-plan.md)
- [etcd-steward repository](https://github.com/gardener/etcd-steward)

---

## 1. Problem Statement

[etcd-backup-restore](https://github.com/gardener/etcd-backup-restore) is the
sidecar container that has managed etcd lifecycle in
[Gardener](https://gardener.cloud) for over five years. It runs alongside every
etcd member as the `backup-restore` container inside the StatefulSet pod. During
that time the component has accumulated responsibilities well beyond its original
scope, resulting in several architectural problems that resist incremental fixes.

### 1.1 Monolithic Design

etcd-backup-restore is a single binary that bundles all of the following into one
process:

| Responsibility | Description |
|---|---|
| Initialization & DB validation | Validates the etcd data directory on startup, detects corruption, determines whether to restore |
| Restoration | Downloads full + delta snapshots from an object store, starts an embedded etcd, replays events |
| Snapshotting (full) | Periodically takes full etcd snapshots and uploads them |
| Snapshotting (delta) | Watches etcd events and batches them into delta snapshots |
| Compaction | Runs an embedded etcd to compact accumulated delta snapshots (also available as a separate Job) |
| Defragmentation | Orchestrates sequential defrag across all cluster members |
| Garbage collection | Deletes old snapshot sets from the object store |
| Leader election | Lease-based leader election to determine which member takes snapshots |
| Health probes | HTTP server with `/healthz` and initialization endpoints |
| Member lease renewal | Renews Kubernetes member leases with member ID and role |
| Snapshot lease renewal | Renews snapshot leases with the latest revision numbers |

This monolithic coupling means that a bug in the defragmenter can crash the
snapshotter, a change to initialization logic risks breaking restoration, and the
test surface of any single component is entangled with all the others.

### 1.2 Overloaded Lease HolderIdentity

Kubernetes Lease objects have a single `Spec.HolderIdentity` field -- a plain
string. etcd-backup-restore encodes multiple pieces of information into this
string:

```
# Member lease HolderIdentity format (backup-restore)
<memberID>:<role>

# Snapshot lease HolderIdentity format (backup-restore)
<latestRevisionNumber>
```

This encoding is fragile. Druid must parse the string, split on `:`, and cast
the parts to the correct types. There are no structured fields for cluster ID,
DB size, snapshot metadata, or state transitions. Any new information requires
extending the string format and updating every consumer.

### 1.3 No Per-Member Kubernetes Resource

etcd-backup-restore publishes member state through leases. This means there is
no Kubernetes resource that captures the full lifecycle of an individual etcd
member -- its state transitions, last restoration, last defragmentation, snapshot
history, or volume health. Without this, etcd-druid operates partially blind: it
can see that a lease exists and parse its HolderIdentity, but it cannot determine
whether a member is in the middle of restoration, waiting to join as a learner,
or has experienced a volume mismatch.

[DEP-04](../proposals/04-etcd-member-custom-resource.md) describes this gap in
detail and proposes the `EtcdMember` custom resource as the solution. However,
implementing DEP-04 on top of the existing backup-restore architecture would
require the sidecar to maintain both the legacy lease protocol and the new CRD
protocol simultaneously, adding more complexity to an already complex codebase.

### 1.4 Initialization Protocol Complexity

The current initialization protocol involves a multi-step handshake between the
etcd-wrapper and etcd-backup-restore:

```
etcd-wrapper                    etcd-backup-restore
    |                                   |
    |--- GET /initialization/status --->|   (poll loop, every 2s)
    |<-- "New" / "Progress" ------------|
    |                                   |
    |--- GET /initialization/start ---->|   (trigger init)
    |<-- 200 OK ------------------------|
    |                                   |
    |    [backup-restore validates DB,  |
    |     restores if needed,           |
    |     starts embedded etcd]         |
    |                                   |
    |--- GET /initialization/status --->|   (poll loop, every 2s)
    |<-- "Successful" ------------------|
    |                                   |
    |--- GET /config ------------------>|   (get etcd config)
    |<-- <etcd-config-yaml> ------------|
    |                                   |
    |    [wrapper starts etcd with      |
    |     config from backup-restore]   |
```

The wrapper polls the sidecar repeatedly, retrieves configuration from it, and
then starts etcd. This means the wrapper -- which should be a thin shell around
embedded etcd -- must understand the initialization state machine and handle
timeout/retry logic for communicating with the sidecar.

### 1.5 Testing Framework

etcd-backup-restore uses [Ginkgo](https://onsi.github.io/ginkgo/) extensively
for testing. While Ginkgo is popular in the Kubernetes ecosystem, the test suite
in etcd-backup-restore suffers from shared state across specs, slow execution
times, and occasional flakiness. The architecture team decided that a ground-up
rewrite is the appropriate time to adopt Go's native `testing` package with
table-driven tests, enabling faster iteration and more reliable CI.

---

## 2. Design Decisions

### 2.1 Decision: Steward Orchestrates, Wrapper Executes

**Context**: In the existing architecture the wrapper initiates contact with the
sidecar by polling `/initialization/status` and `/initialization/start`. The
wrapper must understand when initialization is complete and how to fetch config.
The sidecar is reactive -- it waits for the wrapper to ask.

**Options considered**:

| Option | Description | Verdict |
|---|---|---|
| A. Wrapper drives (status quo) | Wrapper polls sidecar, fetches config, starts etcd | Rejected: wrapper must encode init logic |
| B. Steward drives (chosen) | Steward validates, restores, then POSTs config to wrapper | **Chosen**: clean separation of concerns |
| C. Merge steward into wrapper | Single binary for both etcd and operational tasks | Rejected: wrapper should stay minimal; operational logic would bloat it |

**Decision**: etcd-steward drives the entire initialization flow. The wrapper
exposes two simple HTTP endpoints:

```
POST /embedded-etcd    -- steward sends etcd config YAML, wrapper starts etcd
POST /readyz/set       -- steward tells wrapper to mark itself ready for K8s probes
```

The wrapper no longer needs to understand initialization states, poll endpoints,
or fetch configuration. It starts, waits for a POST, and runs etcd.

The 11-phase startup sequence in etcd-steward (`cmd/etcdsteward/main.go`)
implements this:

```
Phase  1: Start HTTP server (/healthz, /snapshot/*, /config, /initialization/*)
Phase  2: Validate data directory
Phase  3: If corrupt -> removeMember (multi-node) -> deleteDataDir
Phase  4: If empty + backup store -> download full snapshot -> restore via etcdutl
Phase  5: Build etcd config YAML from flags
Phase  6: POST /embedded-etcd to wrapper with config
Phase  7: Wait for etcd to become reachable (poll Get every 2s, 5min timeout)
Phase  8: If restoration happened -> apply delta snapshots via KV client
Phase  9: POST /readyz/set to wrapper -> K8s readiness probe passes
Phase 10: Start runtime components (snapshotter, GC, defrag, alarm, lease, member updater)
Phase 11: Block until SIGTERM
```

Phases 1-9 are sequential -- each must succeed before the next begins. Phase 10
launches all runtime components as concurrent goroutines behind a `sync.WaitGroup`.

**Backward compatibility**: etcd-steward also registers the legacy
`/initialization/status`, `/initialization/start`, and `/config` endpoints so
that the existing etcd-wrapper (`v0.6.2`) can operate in its normal polling mode
without code changes. This dual-mode support means no new wrapper release is
required.

### 2.2 Decision: EtcdMember CRD for State Tracking

**Context**: Member leases store a single string. DEP-04 defines a rich state
model with states (New, Initializing, Starting, Started), sub-states
(DBValidationSanity, DBValidationFull, Restoration, PendingLearner, Learner,
Follower, Leader), and structured fields for snapshots, defragmentation,
restoration, and volume health.

**Decision**: Introduce the `EtcdMember` custom resource with the following
status structure:

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdMember
metadata:
  name: test-0
  namespace: shoot--project--cluster
  ownerReferences:
  - kind: Etcd
    name: test
status:
  id: 128088275939295631
  clusterID: 11588568905070377092
  state: Started
  subState: Leader
  peerTLSEnabled: true
  dbSize: 4294967296      # 4 GiB
  dbSizeInUse: 2147483648 # 2 GiB
  snapshots:
    lastFull:
      timestamp: "2026-04-24T10:00:00Z"
      name: "full-00000000000186a0-00000000000186a0-1714000000.snap.gz"
      size: 104857600
      startRevision: 100000
      endRevision: 100000
    lastDelta:
      timestamp: "2026-04-24T10:05:00Z"
      size: 1048576
      startRevision: 100001
      endRevision: 100500
    accumulatedDeltaSize: 52428800
  transitions:
  - state: New
    reason: NewSingleNodeClusterCreated
    transitionTime: "2026-04-24T09:58:00Z"
  - state: Initializing
    subState: DBValidationFull
    reason: DetectedPreviousUncleanExit
    transitionTime: "2026-04-24T09:58:05Z"
  - state: Started
    subState: Leader
    reason: DBValidationSucceeded
    transitionTime: "2026-04-24T09:58:30Z"
```

The state machine is implemented in
`etcd-steward/internal/statemachine/statemachine.go` and enforces valid
transitions at runtime. Invalid transitions are rejected, preventing bugs where a
member skips from New directly to Started without going through Initializing.

**Who writes what**:
- etcd-steward writes all `EtcdMember.Status` fields (it owns the resource status)
- etcd-druid creates and deletes `EtcdMember` resources (it owns the resource lifecycle)
- etcd-druid reads `EtcdMember.Status` to compute `Etcd.Status.Members` and backup health conditions

### 2.3 Decision: Feature Gate (Alpha -> Beta -> GA)

**Context**: etcd stores the complete Kubernetes cluster state for every Gardener
shoot. A failure in snapshot or restore directly translates to potential shoot
cluster data loss. Rolling out a new sidecar to all production clusters
simultaneously would be reckless.

**Decision**: Introduce the `UseEtcdSteward` feature gate following the same
lifecycle as the proven `UseEtcdWrapper` precedent:

| Phase | Version | Gate State | Duration |
|---|---|---|---|
| Alpha | v0.37.0 (Q2 2026) | Disabled by default, opt-in | 3-4 months |
| Beta | v0.40.0 (Q4 2026) | Enabled by default, opt-out | 3-4 months |
| GA | v0.43.0 (Q2 2027) | Locked to true | 2-3 months |
| Removal | v0.44.0 (Q3 2027) | Gate deleted, old code removed | -- |

The gate is registered in `api/config/v1alpha1/features.go`:

```go
const UseEtcdSteward = "UseEtcdSteward"

func init() {
    DefaultFeatureGates.knownFeatures[UseEtcdSteward] = maturityLevelSpecAlpha
}
```

When the gate is disabled, the system behaves identically to the current master
branch. No new CRDs are created at runtime, no steward image is referenced, and
all existing tests pass without modification.

### 2.4 Decision: Same Pod, Different Sidecar

**Context**: Changing the pod topology (adding new pods, new PVCs, new services)
would multiply the risk surface and require migration tooling.

**Decision**: etcd-steward replaces etcd-backup-restore in the exact same
container slot within the StatefulSet pod. The wrapper container stays the same.
The only change visible to Kubernetes is the container image and command args:

```
# gate=false (backup-restore)
containers:
- name: backup-restore
  image: europe-docker.pkg.dev/.../etcd-backup-restore-distroless:v0.41.1
  command: [etcdbrctl]
  args: [server, --server-port=8080, --storage-provider=S3, ...]

# gate=true (steward)
containers:
- name: backup-restore           # same container name for compatibility
  image: europe-docker.pkg.dev/.../etcd-steward:v0.1.0
  command: [etcd-steward]
  args: [--pod-name=$(POD_NAME), --server-port=8080, --store-provider=S3, ...]
```

No new pods. No new PVCs. No new services. The existing monitoring, alerting,
and log collection that targets the `backup-restore` container continues to work.

---

## 3. High-Level Architecture

### 3.1 Current Architecture (UseEtcdSteward=false)

```
Pod: etcd-main-0
+---------------------------------------------+
|  etcd container (etcd-wrapper v0.6.2)        |
|  - Polls GET /initialization/status          |
|  - Fetches GET /config for etcd YAML         |
|  - Starts embedded etcd with config          |
|  - Readiness: GET /healthz (HTTPS to BR)     |
+---------------------------------------------+
|  backup-restore container (etcdbrctl v0.41)  |
|  - HTTP/HTTPS server (:8080)                 |
|  - Endpoints:                                |
|      /initialization/status                  |
|      /initialization/start                   |
|      /config                                 |
|      /healthz                                |
|  - Initialization:                           |
|      Validate DB -> Restore (embedded etcd)  |
|  - Snapshotter (full + delta)                |
|  - Restorer (embedded etcd based)            |
|  - Compactor (not used at runtime)           |
|  - Defragmenter (cron-scheduled)             |
|  - Garbage collector                         |
|  - Leader election (lease-based)             |
|  - Member lease renewer                      |
|      HolderIdentity: "<memberID>:<role>"     |
|  - Snapshot lease renewer                    |
|      HolderIdentity: "<latestRevision>"      |
+---------------------------------------------+
|  PVC: etcd-main-0                            |
|  /var/etcd/data/new.etcd                     |
+---------------------------------------------+
```

**State reporting**: Member leases + snapshot leases (string-encoded)

**Initialization flow**: Wrapper polls sidecar, sidecar validates/restores, wrapper fetches config

### 3.2 New Architecture (UseEtcdSteward=true)

```
Pod: etcd-main-0
+---------------------------------------------+
|  etcd container (etcd-wrapper v0.6.2)        |
|  - Receives POST /embedded-etcd from steward |
|  - Readiness set via POST /readyz/set        |
|  - Starts embedded etcd with POSTed config   |
|  - Readiness: GET /healthz (HTTP to steward) |
+---------------------------------------------+
|  backup-restore container (etcd-steward v0.1)|
|  - HTTP server (:8080, plain HTTP)           |
|  - Endpoints:                                |
|      /healthz                                |
|      /snapshot/full    (POST)                |
|      /snapshot/delta   (POST)                |
|      /snapshot/latest  (GET)                 |
|      /initialization/* (legacy compat)       |
|      /config           (legacy compat)       |
|  - 11-phase startup:                         |
|      Validate -> Restore (etcdutl) ->        |
|      POST config to wrapper ->               |
|      Wait for etcd -> Apply deltas ->        |
|      Set ready -> Start components           |
|  - Snapshotter (full + delta, etcd lock)     |
|  - Restorer (etcdutl-based, no embedded etcd)|
|  - Defragmenter (etcd lock-based)            |
|  - Garbage collector (count-based)           |
|  - Alarm handler (NOSPACE alarm recovery)    |
|  - Leader watcher (etcd watch-based)         |
|  - Member lease renewer                      |
|      HolderIdentity:                         |
|      "<memberID>:<clusterID>:<role>"         |
|  - EtcdMember status updater (every 10s)     |
|  - State machine (DEP-04 transitions)        |
+---------------------------------------------+
|  PVC: etcd-main-0  (unchanged)               |
|  /var/etcd/data/new.etcd                     |
+---------------------------------------------+
|                                              |
|  EtcdMember CR: etcd-main-0                  |
|  (Kubernetes custom resource, not in pod)    |
|  status:                                     |
|    state: Started                            |
|    subState: Leader                          |
|    id: 128088275939295631                    |
|    clusterID: 11588568905070377092           |
|    snapshots: {lastFull: ..., lastDelta: ...}|
|    transitions: [...]                        |
+---------------------------------------------+
```

**State reporting**: EtcdMember CRD (structured fields) + member leases (backward compat)

**Initialization flow**: Steward validates, restores, POSTs config to wrapper

### 3.3 Component Package Map

etcd-steward is structured as independent internal packages, each owning a
single responsibility:

| Package | Responsibility | Depends On |
|---|---|---|
| `internal/validator` | Data directory validation (sanity + full check) | filesystem |
| `internal/restorer` | Download snapshots, restore via `etcdutl`, apply deltas | snapstore, etcdclient |
| `internal/bootstrapper` | POST config to wrapper, set readiness | HTTP client |
| `internal/snapshotter` | Full + delta snapshot taking with etcd lock | etcdclient, snapstore, compression |
| `internal/snapstore` | Multi-provider object store abstraction | provider SDKs |
| `internal/compression` | Gzip compress/decompress for snapshots | stdlib |
| `internal/defrag` | Sequential defrag across members with etcd lock | etcdclient |
| `internal/gc` | Old snapshot set deletion | snapstore |
| `internal/alarm` | NOSPACE alarm detection and auto-resolution | etcdclient |
| `internal/leaderwatch` | Watch-based leader change detection | etcdclient |
| `internal/lease` | Kubernetes lease renewal (member + snapshot) | k8s client |
| `internal/member` | EtcdMember status updates (info providers) | k8s dynamic client |
| `internal/statemachine` | DEP-04 state transitions (New -> Started) | in-memory |
| `internal/etcdclient` | Typed etcd client wrapper + distributed lock | etcd clientv3 |
| `internal/server` | HTTP server with handler registration | stdlib |
| `internal/config` | CLI flag parsing, config file loading, validation | cobra/pflag |

Each package exposes a constructor and a `Run(ctx context.Context)` method. The
main function in `cmd/etcdsteward/main.go` wires them together.

---

## 4. Compatibility Contract

### 4.1 Behavioral Equivalence

When `UseEtcdSteward=false`: the system is identical to the current master
branch. No new CRDs are consulted, no steward code paths execute, all existing
tests pass. This is enforced by CI running the full test suite with the gate
disabled on every PR.

When `UseEtcdSteward=true`: etcd-steward runs instead of etcd-backup-restore.
The observable behavior must be equivalent:

| Capability | backup-restore | steward | Notes |
|---|---|---|---|
| Full snapshot taking | Periodic + on-demand (GET) | Periodic + on-demand (POST) | HTTP method differs |
| Delta snapshot taking | Event-watcher based | Event-watcher based | Same mechanism |
| Restore from backup | Embedded etcd in sidecar | `etcdutl` restore + delta replay | No embedded etcd needed |
| Defragmentation | Cron-scheduled by sidecar | Interval-based with etcd lock | Lock prevents concurrent defrag |
| Garbage collection | Policy-based (Exponential/LimitBased) | Count-based (max full snapshots) | Simplified in steward |
| Member lease renewal | `<memberID>:<role>` | `<memberID>:<clusterID>:<role>` | 3-part format adds clusterID |
| Snapshot lease renewal | `<revision>` in HolderIdentity | Same format (compatibility mode) | Steward renews if flag set |
| Readiness probe | HTTPS to wrapper `/healthz` | HTTP to steward `/healthz` | No TLS between sidecar and wrapper |
| Pod startup time | ~25s (single member) | ~25s (single member) | Equivalent |

### 4.2 Snapshot Format Compatibility

etcd-steward produces snapshots in the exact same format as etcd-backup-restore:

- **Full snapshots**: etcd `Snapshot` API output, gzip-compressed, uploaded with
  the same naming convention (`full-<startRev>-<endRev>-<timestamp>.snap.gz`)
- **Delta snapshots**: Serialized etcd `Watch` events, gzip-compressed, same
  naming convention (`delta-<startRev>-<endRev>-<timestamp>.snap.gz`)
- **Snapstore layout**: Same directory structure in the object store

This bidirectional compatibility means:
- A snapshot taken by backup-restore can be restored by steward
- A snapshot taken by steward can be restored by backup-restore
- Rollback from steward to backup-restore does not require re-snapshotting

### 4.3 Toggle Verification

The feature gate toggle has been validated end-to-end:

**Test: steward -> backup-restore -> verify data integrity**

1. Deploy Etcd with `UseEtcdSteward=true`. Steward starts, takes snapshots,
   EtcdMember shows `State=Started, SubState=Leader`. Pod ready at T=25s.
2. Set `UseEtcdSteward=false`, redeploy druid, trigger reconcile.
3. StatefulSet rolling-updates: steward image replaced with backup-restore image.
4. backup-restore starts, validates existing data directory, resumes snapshotting.
5. All conditions healthy: `AllMembersReady=True`, `Ready=True`. Pod ready at
   T=40s (15s rollout + 25s startup).

**Result**: Zero data loss. Seamless sidecar switch. No manual intervention
required.

---

## 5. What Changes in etcd-druid

The etcd-druid codebase is gated on `UseEtcdSteward` in approximately 20
locations across 15 files. The key changes are:

| Area | gate=false (backup-restore) | gate=true (steward) |
|---|---|---|
| Sidecar image | `etcd-backup-restore-distroless` | `etcd-steward` |
| Sidecar command | `etcdbrctl server --server-port=8080 ...` | `etcd-steward --pod-name=$(POD_NAME) ...` |
| Wrapper readiness | HTTPS probe to wrapper `/healthz` | HTTP probe to steward `/healthz` |
| Wrapper TLS flag | `--backup-restore-tls-enabled=true` | `--backup-restore-tls-enabled=false` |
| Snapshot leases | Created and renewed | Skipped (tracked in EtcdMember) |
| EtcdMember CRs | Not created | Created per replica, updated by steward |
| Compaction trigger | Read snapshot revision from leases | Read from `EtcdMember.Status.Snapshots` |
| Full snapshot HTTP | `GET /snapshot?mode=full` (HTTPS) | `POST /snapshot/full` (HTTP) |
| Etcd status source | Parse member lease HolderIdentity | Read `EtcdMember.Status.State` |
| RBAC (per-etcd Role) | No etcdmember rules | `etcdmembers` and `etcdmembers/status` verbs |
| Sync order | ConfigMap -> StatefulSet | ConfigMap -> EtcdMember -> StatefulSet |

---

## 6. Known Gaps

These are tracked gaps that must be resolved before the feature can graduate
beyond alpha:

| Gap | Priority | Description | Blocks |
|---|---|---|---|
| Compact subcommand | P0 | Steward needs a `compact` subcommand for the compaction Job | Beta |
| Steward HTTP TLS | P1 | Some environments require TLS on all sidecar endpoints | Beta (TLS-strict) |
| BackupReady condition | P1 | Health checker must read EtcdMember snapshots instead of snapshot leases | Beta |
| Defrag scheduling | P2 | Steward's defrag is wired but not scheduled at runtime | GA |
| Monitoring tool update | P2 | Legacy monitoring reads snapshot lease HolderIdentity | GA |

---

## 7. Why Not Iterate on etcd-backup-restore?

This is the natural question: why replace instead of refactor?

**Structural coupling**: The initialization flow, restoration, and snapshotting
share a single embedded etcd instance inside the sidecar. Decoupling them
requires rearchitecting the core of backup-restore. The initialization protocol
is deeply entwined with the wrapper's polling loop, making it impossible to
change one without changing the other.

**Lease-based state is a dead end**: DEP-04 was approved in 2023 with the
explicit goal of replacing lease-based state reporting with the EtcdMember CRD.
Implementing DEP-04 inside backup-restore would require the sidecar to maintain
both protocols indefinitely during the transition, doubling the state management
complexity.

**Testing foundation**: etcd-backup-restore's Ginkgo test suite has accumulated
shared state, complex setup/teardown, and integration tests that require running
embedded etcd. Starting fresh with Go native tests and clear package boundaries
enables faster iteration and more reliable CI.

**Scope of change**: A line-by-line comparison shows that the sidecar's public
interface (CLI args, HTTP endpoints, snapshot format, lease protocol) must change
in at least 15 places to support the steward architecture. At that point, the
delta between "refactor backup-restore" and "write steward" is small, and steward
has the advantage of a clean dependency graph.

The [UseEtcdWrapper precedent](#decision-3-feature-gate-alpha--beta--ga) proves
this pattern works: introduce a new component behind a feature gate, validate it
over 15+ months, then remove the old code. UseEtcdWrapper took 19 months from
alpha to gate removal. UseEtcdSteward follows the same lifecycle with a projected
timeline of 15-18 months.

---

## 8. Summary

| Aspect | etcd-backup-restore | etcd-steward |
|---|---|---|
| Architecture | Monolithic, all responsibilities in one process | Modular packages with explicit `Run(ctx)` contracts |
| Initialization | Wrapper polls sidecar (reactive) | Steward POSTs to wrapper (proactive) |
| State tracking | Lease HolderIdentity strings | EtcdMember CRD with structured fields |
| State machine | Implicit (code flow determines state) | Explicit (DEP-04 transitions enforced at runtime) |
| Restoration | Embedded etcd inside sidecar | `etcdutl` restore + KV client delta replay |
| Leader detection | Lease-based leader election | etcd watch-based leader observation |
| Locking | None (race conditions possible in multi-node) | etcd distributed locks for snapshot and defrag |
| Testing | Ginkgo/Gomega | Go native `testing` with table-driven tests |
| Rollout | N/A (existing) | Feature gate: Alpha -> Beta -> GA -> Removal |
| Rollback | N/A | Toggle gate off, 40s recovery, zero data loss |
