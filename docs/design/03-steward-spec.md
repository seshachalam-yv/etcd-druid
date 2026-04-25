# Spec: etcd-steward Sidecar Internal Design

## Status

Draft -- 2026-04-24

## Overview

etcd-steward is a sidecar container that runs alongside the etcd-wrapper in each
etcd StatefulSet pod. It implements the "Steward Orchestrates, Wrapper Executes"
architecture (Notes Option-1): the steward drives the entire lifecycle -- data
validation, restoration, etcd startup, snapshots, defrag, alarms, lease
renewal, and member status reporting -- while the wrapper is a thin process that
embeds and runs etcd on demand.

## Package Overview

20 packages, each with a single responsibility:

| Package | Responsibility |
|---------|----------------|
| `cmd/etcdsteward/` | Daemon entry point + `compact` and `copybackups` subcommands |
| `internal/alarm/` | Polls etcd AlarmList; auto-resolves NOSPACE (compact + defrag + disarm) |
| `internal/bootstrapper/` | HTTP client for etcd-wrapper (`POST /embedded-etcd`, `POST /readyz/set`) |
| `internal/compactor/` | Offline compaction logic (used by `compact` subcommand) |
| `internal/compression/` | gzip compress/decompress for snapshot data |
| `internal/config/` | Configuration model, CLI flag binding, YAML file loading, validation |
| `internal/defrag/` | Coordinated defragmentation across cluster members (leader-only) |
| `internal/errors/` | Structured error codes with code, sub-code, cause, and operation |
| `internal/etcdclient/` | Thin typed wrapper around `clientv3` (KV, Maintenance, Lock) |
| `internal/gc/` | Snapshot garbage collection with count-based and calendar retention policies |
| `internal/leaderwatch/` | Leadership observation via polling `/steward/leader` etcd key |
| `internal/lease/` | Kubernetes coordination lease renewal with configurable HolderIdentity |
| `internal/member/` | EtcdMember status updater, info providers, K8s state recorder |
| `internal/metrics/` | Prometheus metrics registration |
| `internal/restorer/` | Full snapshot restore via `etcdutl` + delta replay via KV client |
| `internal/server/` | HTTP server (`/healthz`, `/metrics`, `/snapshot/*`, `/config`) |
| `internal/snapshotter/` | Periodic full + delta snapshot taking with distributed lock |
| `internal/snapstore/` | Snapshot storage abstraction (Local, S3, GCS, ABS, OSS, Swift) |
| `internal/statemachine/` | DEP-04 lifecycle state machine + K8s recorder |
| `internal/validator/` | Data directory integrity checks (bbolt page scan) |

## Configuration Model

All configuration lives in `config.Config`, a flat struct with 45+ fields
resolved in three layers (highest priority first): CLI flags via `pflag`,
YAML config file via `viper` (applied only when the flag was not explicitly
set), and defaults from `config.DefaultConfig()`.

Key defaults: `ServerPort=8080`, `DataDir=/var/etcd/data/new.etcd`,
`EmbeddedEtcdQuotaBytes=8GiB`, `FullSnapshotInterval=24h`,
`DeltaSnapshotInterval=5m`, `GCPeriod=1h`, `DefragInterval=24h`,
`K8sHeartbeatDuration=2s`, `MaxFullSnapshots=7`,
`WrapperURL=http://localhost:9095`, `StoreProvider=Local`.

Validation checks `PodName`, `PodNamespace`, and `EtcdEndpoints` are non-empty,
port is in range, and `FullSnapshotInterval >= DeltaSnapshotInterval`.

## Startup Flow (11 Phases)

The `runDaemon` function implements the complete startup sequence.

### Phase 1: Parse config + create logger

Loads optional YAML config file, validates the merged config, creates a
production zap logger. **Failure**: fatal; pod enters CrashLoopBackOff.

### Phase 2: Create snapstore (L1: nil interface gotcha)

```go
var store snapstore.SnapStore             // interface, zero value is nil
localStore, snapErr := snapstore.NewSnapStore(cfg.StoreProvider, ...)
if snapErr != nil {
    logger.Warn("failed to create snapstore")
} else {
    store = localStore                    // only assign if non-nil
}
```

**Critical pattern (L1):** Two-step assignment avoids the Go nil-interface
gotcha. A nil concrete value behind a non-nil interface would cause panics in
`if store != nil` guards. **Failure**: Non-fatal; snapshotter, GC, and restore
are disabled when `store == nil`.

### Phase 3: Create validator + restorer

Instantiates `validator.New(dataDir)` always and `restorer.New(store, ...)`
only when `store != nil`. Construction cannot fail.

### Phase 4: Signal handling

Installs SIGTERM/SIGINT handler that cancels the root context.

### Phase 5: Create state machine + trigger New transition

Creates the DEP-04 state machine early. Since the K8s dynamic client is not yet
available, transitions are buffered in `pendingTransitions` and flushed later.

```go
sm := statemachine.New(isSingleNode)
// Single-node: ReasonNewSingleNodeClusterCreated -> (New, "")
// Multi-node:  ReasonClusterScaledUp             -> (New, "")
```

### Phase 6: Start HTTP server + register endpoints

Starts the HTTP server in a background goroutine. Registers `/healthz`,
`/metrics`, `/initialization/status` and `/initialization/start` (backward
compat), `/config`, `/snapshot/full` (POST), `/snapshot/delta` (POST),
`/snapshot/latest` (GET). Snapshot endpoints hold a nil snapshotter until
Phase 11b wires the live instance. **Failure**: fatal if bind fails.

### Phase 7: Validate data directory

If non-empty, triggers `validator.FullCheck` (opens bbolt read-only, iterates
every bucket and key). DEP-04: `New -> Initializing/DBValidationFull`.
**Failure**: Non-fatal; sets corrupt flag for Phase 8.

### Phase 8: If corrupt, clean data directory

Removes and re-creates the data directory. DEP-04: single-node goes to
`Initializing/Restoration`; multi-node goes to `New`. **Failure**: fatal if
filesystem operations fail.

### Phase 9: If empty + backup exists, restore from backup

Downloads the latest full snapshot and restores via `etcdutl/snapshot.Restore`.
Uses panic recovery because etcdutl calls `zap.Logger.Panic` on DB failures:

```go
func safeRestore(mgr snapshot.Manager, cfg snapshot.RestoreConfig) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("restore panicked: %v", r)
        }
    }()
    return mgr.Restore(cfg)
}
```

Sets `needsRestore = true` for delta replay after etcd starts. **Failure**:
non-fatal; cleans data dir and starts fresh.

### Phase 10: Build etcd config + POST /embedded-etcd to wrapper

Generates flat etcd-native YAML from flags (not the druid-mounted ConfigMap,
which uses a per-member URL map format the wrapper cannot parse). Sends config
to the wrapper via `bootstrapper.NewHTTPWrapperClient(cfg.WrapperURL)`.
TLS config is built from flag paths (`--ca-cert`, `--cert`, `--key`).
**Failure**: non-fatal; wrapper may already have etcd running.

### Phase 11: Wait for etcd ready + start runtime components

Polls etcd with `Get("health")` every 2s, 5-minute timeout. Then: creates
`clientv3` client, replays deltas if restored, sends `POST /readyz/set` to
wrapper, creates K8s clients (best-effort), starts 8 runtime component groups,
blocks on `<-ctx.Done()` with 30s graceful shutdown.

## Runtime Components

### Leader Watcher (`leaderwatch.Watcher`)

Polls etcd key `/steward/leader` at `LeaderElectionReelectionPeriod` (default
5s). Maintains `Role` enum (`Leader`, `Follower`, `Unknown`) and notifies
subscribers via buffered channels on role changes.

### Snapshotter (`snapshotter.Snapshotter`)

Acquires distributed lock `/steward/snapshot-lock` (TTL 30s). Takes initial
full snapshot, then runs `FullSnapshotInterval` and `DeltaSnapshotInterval`
tickers. Full snapshots stream via `Maintenance.Snapshot`, compress with gzip,
upload. Delta snapshots watch etcd events from `lastDeltaRev + 1`, serialize
as compressed NDJSON, stop at 100 MiB or target revision.

### Garbage Collector (`gc.GarbageCollector`)

Runs at `GCPeriod` (1h). Groups snapshots into `SnapshotSet`s (full + trailing
deltas). Retains at most `MaxFullSnapshots` (7) sets; latest is never deleted.
Supports pluggable `RetentionPolicy` including `CalendarPolicy`.

### Defragmenter (`defrag.Defragmenter`)

Leader-only, at `DefragInterval` (24h). Uses distributed lock
`/steward/defrag-lock`. Defrags followers first, leader last (endpoints sorted
so local is last). Tracks status via `/steward/defrag/status/` keys. Exposes
`DefragDirect` for NOSPACE emergency bypass.

### Alarm Handler (`alarm.Handler`)

Polls `AlarmList` at `AlarmPollInterval` (30s). NOSPACE: compact at
`revision - CompactRevisionLag`, defrag all endpoints, disarm. CORRUPT: log
error for manual intervention.

### Member Lease Renewer (`lease.Renewer`)

Renews coordination lease at `K8sHeartbeatDuration` (2s). HolderIdentity
uses 3-part format matching druid's `readyCheck` expectations:

```go
leaseRenewer.SetHolderIdentityFunc(func(ctx context.Context) string {
    resp, _ := etcdClient.Status(ctx, cfg.EtcdEndpoints[0])
    role := "Member"
    if resp.Leader == resp.Header.MemberId { role = "Leader" }
    return fmt.Sprintf("%d:%d:%s", resp.Header.MemberId, resp.Header.ClusterId, role)
})
```

When peer TLS is enabled, sets annotation
`member.etcd.gardener.cloud/tls-enabled: "true"`. Lease duration = `3 * heartbeat`.

### Snapshot Lease Renewers

Two separate `lease.Renewer` instances for `FullSnapshotLeaseName` and
`DeltaSnapshotLeaseName`. Simple heartbeat renewers without custom
HolderIdentity logic.

### EtcdMember Updater (`member.Updater`)

Ticks at `K8sHeartbeatDuration` (2s). Queries `MaintenanceStatusProvider`
(member ID, cluster ID, role, DB size) and `SnapshotInfoProvider` (latest
snapshot metadata), passes combined `MemberInfo` to `K8sStateRecorder` which
patches the EtcdMember status sub-resource. Also flushes buffered
`pendingTransitions` from Phase 5.

**Immediate sync after on-demand snapshot:** The `/snapshot/full` and
`/snapshot/delta` HTTP handlers call `updater.RecordStateTransition()`
synchronously after a successful snapshot, bypassing the 2s tick.

## State Machine (DEP-04)

Tracks each member's lifecycle as a `(State, SubState)` pair. Transitions are
validated against a pre-built rule table. All operations are mutex-protected.

### States and SubStates

| State | SubStates | Description |
|-------|-----------|-------------|
| `New` | (none) | Freshly created, not yet initialized |
| `Initializing` | `DBValidationSanity`, `DBValidationFull`, `Restoration` | DB validation or restoration |
| `Starting` | `PendingLearner`, `Learner` | Joining cluster as learner (multi-node) |
| `Started` | `Leader`, `Follower` | Fully participating in cluster |

### Transition Rules

```
Source                          Reason                          Target
-----------------------------------------------------------------------
("", "")                        ClusterScaledUp                (New, "")
("", "")                        NewSingleNodeClusterCreated    (New, "")
(New, "")                       DetectedPreviousCleanExit      (Initializing, DBValidationSanity)
(New, "")                       DetectedPreviousUncleanExit    (Initializing, DBValidationFull)
(New, "")                       WaitingToJoinAsLearner         (Starting, PendingLearner)
(Initializing, DBValidation*)   DBValidationFailed             Single: (Initializing, Restoration)
                                                               Multi:  (New, "")
(Initializing, DBValidation*)   DBValidationSucceeded          Single: (Started, Leader)
                                                               Multi:  (Started, Follower)
(Initializing, Restoration)     RestorationSucceeded           (Started, Leader)
(Starting, PendingLearner)      JoinedAsLearner                (Starting, Learner)
(Starting, Learner)             PromotedAsVotingMember         (Started, Follower)
(Started, Follower)             GainedClusterLeadership        (Started, Leader)
(Started, Leader)               LostClusterLeadership          (Started, Follower)
```

Topology-dependent rules are resolved at construction based on `isSingleNode`.
Invalid transitions return `ErrCodeInvalidTransition`.

### Transition Recording

Two recorders: `statemachine.K8sRecorder` (init-time, flushes buffered
transitions to `status.state`/`status.lastTransition`) and
`member.K8sStateRecorder` (runtime, patches full status including `id`,
`clusterID`, `dbSize`, `state`, `transitions` array, and `snapshots`).

## Key Design Patterns

### L1: Nil Interface Gotcha

Two-step assignment prevents a nil concrete pointer from producing a non-nil
interface. See Phase 2 code above.

### Panic Recovery in Restorer

Wraps `etcdutl/snapshot.Restore` with `recover()` because the library uses
`zap.Panic`. See Phase 9 code above.

### TLS Config from Flags

Generated from steward's own flags, not the druid-mounted ConfigMap which uses
a format the wrapper cannot parse.

### Immediate EtcdMember Sync After On-Demand Snapshot

HTTP handlers call `updater.RecordStateTransition()` synchronously, not waiting
for the 2s tick. Ensures EtcdMember CR reflects snapshot metadata immediately
when the compaction controller triggers `POST /snapshot/full`.

### Adapter Pattern for Client Interfaces

Small adapter structs (`defragMaintenanceAdapter`, `defragKVAdapter`,
`defragClusterAdapter`, `memberStatusAdapter`, `leaderRoleProvider`) bridge
the typed `etcdclient.Client` to narrow component interfaces, keeping each
component's dependency surface minimal and testable.

### Buffered Transition Flushing

Init-time transitions (Phases 5-9) accumulate in `pendingTransitions`. After
the K8s dynamic client is created (Phase 11h), all are flushed:

```go
for _, pt := range pendingTransitions {
    smRecorder.Record(ctx, cfg.PodName, cfg.PodNamespace, pt)
}
pendingTransitions = nil
```

### Distributed Lock for Single-Writer Semantics

`/steward/snapshot-lock` (held for entire snapshot lifecycle) and
`/steward/defrag-lock` (acquired per-member) backed by etcd concurrency
sessions ensure only one replica operates at a time.

### Graceful Shutdown

SIGTERM cancels root context. All components detect `<-ctx.Done()`.
`sync.WaitGroup` tracks goroutines with a 30-second drain timeout.

## Error Model

21 structured error codes (`ErrCodeConfig`, `ErrCodeIO`, `ErrCodeNetwork`,
`ErrCodeEtcd`, `ErrCodeSnapshot`, `ErrCodeRestore`, `ErrCodeStorage`,
`ErrCodeValidation`, `ErrCodeInvalidTransition`, etc.) in `internal/errors`.
Each `Error` carries code, sub-code, cause (supports `errors.Unwrap`), message,
and operation.

## Data Flow Summary

```
Pod Start
  |
  v
[Phase 1-4] Config + Logger + Signals
  |
  v
[Phase 5] State Machine: ("", "") -> (New, "")
  |
  v
[Phase 6] HTTP Server starts (8080)
  |
  v
[Phase 7-8] Validate data dir -> clean if corrupt
  |
  v
[Phase 9] Restore from backup (if empty + store available)
  |
  v
[Phase 10] POST /embedded-etcd -> wrapper (9095)
  |
  v
[Phase 11] Poll etcd (2s) until ready (5min timeout)
  |
  +-- Apply delta snapshots (if restored)
  +-- POST /readyz/set -> wrapper
  +-- Create K8s clients
  +-- Start 8 runtime component groups
  +-- Block on <-ctx.Done()
  |
  v
[Shutdown] Cancel context -> 30s WaitGroup drain -> exit
```
