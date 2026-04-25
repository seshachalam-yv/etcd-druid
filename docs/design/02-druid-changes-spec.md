# Spec: etcd-druid Changes for UseEtcdSteward

## Status

Draft -- 2026-04-24

## Scope

This document covers ONLY etcd-druid changes required to support etcd-steward as an alternative
sidecar to etcd-backup-restore. See `03-steward-spec.md` for etcd-steward internals and
`04-wrapper-spec.md` for etcd-wrapper changes.

All changes are gated behind the `UseEtcdSteward` alpha feature gate and have zero impact on
existing etcd-backup-restore deployments when the gate is disabled.

---

## 1. Feature Gate

**What**: Added `UseEtcdSteward` alpha feature gate in `api/config/v1alpha1/features.go`, registered as `maturityLevelSpecAlpha` in `init()`.

**Why**: Feature gates are the standard Gardener mechanism for incremental rollout. Alpha means disabled by default, opt-in only. Intentionally alpha (not beta) because etcd-steward replaces the data backup component -- maximum caution is warranted. Operators must explicitly enable via `--feature-gates=UseEtcdSteward=true`.

```go
const UseEtcdSteward = "UseEtcdSteward"

func init() {
    DefaultFeatureGates.knownFeatures[UseEtcdSteward] = maturityLevelSpecAlpha
}
```

**Files**: `api/config/v1alpha1/features.go`

---

## 2. EtcdMember CRD

**What**: New CRD `EtcdMember` (`api/core/v1alpha1/etcdmember_types.go`) with:
- **MemberState**: `New | Initializing | Starting | Started`
- **MemberSubState**: `DBValidationSanity | DBValidationFull | Restoration | PendingLearner | Learner | Follower | Leader`
- **EtcdMemberResourceStatus**: typed fields for `ID`, `ClusterID`, `State`, `SubState`, `PeerTLSEnabled`, `DBSize`, `Snapshots`, `LastRestoration`, `LastDefragmentation`, `Transitions`
- **MemberSnapshotStatus**: `LastFull` / `LastDelta` as `SnapshotInfo` with typed `EndRevision`, `Name`, `Timestamp`, `Size`

**Why**: backup-restore encoded member status into lease `HolderIdentity` as a plain string. This was overloaded and schema-less. EtcdMember provides typed fields, an explicit lifecycle state machine, structured snapshot tracking, and operational history.

**Design Decision -- Per-member CR vs single status**: We chose per-member CRs (one per pod) because each sidecar can independently patch its own CR via `/status` with no write conflicts, and CR lifecycle naturally follows pod lifecycle.

**Component operator** (`internal/component/etcdmember/etcdmember.go`): follows the standard Operator interface. `Sync` creates one EtcdMember per replica, annotates new members during scale-up with `druid.gardener.cloud/create-as-learner`, and deletes excess members during scale-down. Registered conditionally and synced **before** StatefulSet so CRs exist before pods start:

```go
// In reconcile_spec.go -- sync ordering
if druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    operators = append(operators, component.EtcdMemberKind)
}
operators = append(operators, component.StatefulSetKind)
```

EtcdMember is also added to the cleanup list so CRs are deleted when the Etcd resource's
`operation.gardener.cloud/create-runtime-components` annotation is removed.

**Files**: `api/core/v1alpha1/etcdmember_types.go`, `internal/component/etcdmember/etcdmember.go`, `internal/component/registry.go`, `internal/common/constants.go`, `internal/controller/etcd/reconciler.go`, `internal/controller/etcd/reconcile_spec.go`

---

## 3. StatefulSet Builder

Changes in `internal/component/statefulset/builder.go`:

### 3a. Steward container args

New `getStewardContainerCommandArgs()` produces flags for etcd-steward. Key differences from backup-restore:

| Flag | backup-restore | etcd-steward |
|------|---------------|-------------|
| First arg | `server` (subcommand) | none (single binary) |
| Store provider | `--storage-provider` | `--store-provider` |
| Server TLS | not needed | `--etcd-server-cert/key` |
| Peer TLS | not needed | `--peer-ca-cert/server-cert/server-key` |
| Snapshot schedule | `--schedule`, `--delta-snapshot-period` | none (internal scheduling) |

**Why**: etcd-steward generates the full etcd configuration file (including TLS blocks), so it needs server and peer certificates that backup-restore never needed.

### 3b. Volume mounts

Added `VolumeNameEtcdServerTLS` (when `ClientUrlTLS != nil`) and `VolumeNameEtcdPeerCA` + `VolumeNameEtcdPeerServerTLS` (when `PeerUrlTLS != nil`) to the backup-restore container:

```go
if druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) &&
    b.etcd.Spec.Etcd.ClientUrlTLS != nil {
    brVolumeMounts = append(brVolumeMounts, corev1.VolumeMount{
        Name:      common.VolumeNameEtcdServerTLS,
        MountPath: common.VolumeMountPathEtcdServerTLS,
    })
}
```

**Critical placement decision**: These mounts are appended **outside** `getBackupRestoreContainerSecretVolumeMounts()`. The `handleTLSChanges()` function compares secret volume mounts between desired and existing STS to detect TLS drift. If our mounts were inside that function, every reconcile would see a mismatch (steward has more mounts than the existing STS) and trigger a rolling update.

### 3c. Readiness probe

```go
if druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    return corev1.ProbeHandler{
        HTTPGet: &corev1.HTTPGetAction{
            Path:   "/healthz",
            Port:   intstr.FromInt32(b.backupPort),
            Scheme: corev1.URISchemeHTTP,
        },
    }
}
```

**Why**: backup-restore uses HTTPS on port 8080. Steward uses HTTP on port 8080. The probe scheme must match, otherwise kubelet marks the container not-ready.

### 3d. Wrapper `--backup-restore-tls-enabled=false`

```go
if b.etcd.Spec.Backup.TLS == nil ||
    druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    commandArgs = append(commandArgs, "--backup-restore-tls-enabled=false")
}
```

**Why**: etcd-wrapper uses this flag to decide HTTP vs HTTPS for sidecar communication. Steward serves plain HTTP, so this must be false regardless of whether `Backup.TLS` secrets exist.

---

## 4. Status Reconciliation

**What**: New `mutateETCDStatusFromEtcdMembers()` in `internal/controller/etcd/reconcile_status.go`, called when gate is enabled. Lists owned EtcdMember CRs and maps state to `Etcd.Status.Members`.

**State mapping**:

| EtcdMember.Status.State | EtcdMemberConditionStatus | Role (when Started) |
|------------------------|--------------------------|---------------------|
| `Started` | `Ready` | Leader or Member (from SubState) |
| `Starting` | `NotReady` | -- |
| `Initializing` | `NotReady` | -- |
| `New` / nil | `Unknown` | -- |

**Fallback**: If all owned EtcdMembers have empty `Status.State` (steward not started yet), falls back to lease-based status. The steward sets member lease HolderIdentity in `<memberID>:<role>` format, so the legacy path works during the transition window.

```go
if populatedCount == 0 {
    logger.Info("All EtcdMember resources have empty status, falling back to member-lease-based status check")
    statusCheck := status.NewChecker(r.client, ...)
    if err := statusCheck.Check(ctx, logger, etcd); err != nil { ... }
    return ctrlutils.ContinueReconcile()
}
```

After populating members, delegates to the newly exported `status.Checker.ExecuteConditionChecks()` for condition computation (AllMembersReady, ReadyReplicas, etc.).

**Files**: `internal/controller/etcd/reconcile_status.go`, `internal/health/status/check.go`

---

## 5. Compaction Controller

Three changes in `internal/controller/compaction/`:

**5a. Snapshot revision source** (`reconciler.go`): New `getDeltaRevisionsSinceFullSnapshotFromEtcdMembers()` lists owned EtcdMember resources and finds the one with populated Snapshots (only the leader's sidecar takes snapshots). Computes delta:

```go
fullRevision := *snapshots.LastFull.EndRevision
deltaRevision := *snapshots.LastDelta.EndRevision
return deltaRevision - fullRevision, nil
```

**5b. Event predicate** (`register.go`): New `etcdMemberSnapshotChanged()` predicate uses `reflect.DeepEqual` on `Status.Snapshots` to trigger compaction when snapshot info changes. Controller conditionally `Owns(&druidv1alpha1.EtcdMember{})`.

**5c. Full snapshot HTTP** (`snapshot.go`): Uses `POST` (not `GET`) for steward's RESTful API. `newHTTPClient()` skips TLS setup -- plain HTTP:

```go
if tlsConfig := etcd.Spec.Backup.TLS; tlsConfig != nil &&
    !druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    httpScheme = "https"
    // ... TLS setup
}
```

**Why**: backup-restore stored snapshot revisions in lease HolderIdentity (plain int64 string). Steward uses typed fields in EtcdMember.Status.Snapshots. POST is semantically correct for a state-changing operation.

**Files**: `internal/controller/compaction/reconciler.go`, `register.go`, `snapshot.go`

---

## 6. Peer TLS Handling

**What**: Skip `IsPeerURLInSyncForAllMembers()` check in `handleTLSChanges()` when steward is enabled:

```go
if peerTLSInSyncForAllMembers ||
    druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    r.logger.Info("Peer URL TLS configuration is reflected on all currently running members")
    return nil
}
```

**Why**: backup-restore sets `member.etcd.gardener.cloud/tls-enabled` annotation on member leases. druid waits for all members to have this annotation before completing the STS sync. Steward manages peer TLS through its own config generation and reports `PeerTLSEnabled` in EtcdMember.Status. The lease annotation check was blocking the initial STS sync because steward sets the annotation via a different mechanism.

**Files**: `internal/component/statefulset/statefulset.go`

---

## 7. RBAC

**What**: In `internal/component/role/role.go`:
1. Added `create` verb for leases (steward creates snapshot leases on first snapshot)
2. Added `etcdmembers` (get/list/watch) and `etcdmembers/status` (get/patch/update) rules

```go
{
    APIGroups: []string{"coordination.k8s.io"},
    Resources: []string{"leases"},
    Verbs:     []string{"create", "get", "list", "patch", "update", "watch"},  // added: create
},
{
    APIGroups: []string{"druid.gardener.cloud"},
    Resources: []string{"etcdmembers"},
    Verbs:     []string{"get", "list", "watch"},
},
{
    APIGroups: []string{"druid.gardener.cloud"},
    Resources: []string{"etcdmembers/status"},
    Verbs:     []string{"get", "patch", "update"},
},
```

**Why**: backup-restore only needed get/list/patch/update/watch on leases because druid pre-created them. Steward also creates snapshot leases (the leader creates them on first snapshot). The EtcdMember rules allow the sidecar to read its CR and patch the `/status` subresource to report state, snapshots, and transitions.

---

## 8. Snapshot Lease Skip

**What**: `snapshotlease.Sync()` returns early when gate is enabled:

```go
func (r _resource) Sync(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) error {
    if druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
        ctx.Logger.Info("UseEtcdSteward is enabled, skipping snapshot lease creation")
        return nil
    }
    // ... legacy lease creation path
}
```

**Why**: Steward tracks snapshots via `EtcdMember.Status.Snapshots`. Snapshot leases are not needed. Compaction reads from EtcdMember when gate=true (see section 5). Skipping lease creation avoids creating resources that would never be updated and could confuse operators inspecting the cluster.

**Files**: `internal/component/snapshotlease/snapshotlease.go`

---

## 9. Image Vector

**What**: New `etcd-steward` entry in `internal/images/images.yaml`. New `ImageKeyEtcdSteward` constant. `getEtcdImageKeys()` returns the steward image key instead of backup-restore when gate is enabled.

**Why**: The image vector is druid's mechanism for resolving container images. The steward image must be in the vector so `IMAGEVECTOR_OVERWRITE` can override it in Gardener landscapes and e2e tests.

**Files**: `internal/images/images.yaml`, `internal/common/constants.go`, `internal/utils/image.go`, `test/utils/constants.go`, `test/utils/imagevector.go`

---

## 10. EtcdOpsTask HTTP Client

**What**: `ConfigureHTTPClientForEtcdBR()` skips TLS when steward is enabled, returning plain HTTP:

```go
if tlsConfig == nil ||
    druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    return defaultClient, "http", nil
}
```

**Why**: The EtcdOpsTask controller uses this for on-demand snapshots (`POST /snapshot/full`). Steward's server is plain HTTP, so TLS must be skipped even if `Backup.TLS` secrets exist in the Etcd spec. Without this change, the ops task controller would attempt HTTPS against steward's HTTP port and fail with a TLS handshake error.

**Files**: `internal/controller/etcdopstask/handler/utils/etcdbrclient.go`

---

## 11. EtcdOpsTask Deletion Predicate (Bug Fix)

**What**: New `markedForDeletionPredicate()` fires on UPDATE when `DeletionTimestamp` transitions from nil to non-nil:

```go
func markedForDeletionPredicate() predicate.Predicate {
    return predicate.Funcs{
        UpdateFunc: func(e event.UpdateEvent) bool {
            return e.ObjectOld.GetDeletionTimestamp() == nil &&
                   e.ObjectNew.GetDeletionTimestamp() != nil
        },
    }
}
```

**Why**: Setting `DeletionTimestamp` does not increment `metadata.generation`, so `GenerationChangedPredicate` filtered out force-delete events. Without this predicate, an EtcdOpsTask stuck in Terminating would never be reconciled. Not steward-specific but discovered during steward development when pre-sync snapshot tasks needed reliable cleanup.

**Files**: `internal/controller/etcdopstask/register.go`

---

## 12. Pre-Sync Task TTL (Bug Fix)

**What**: `preSyncTaskTTLSeconds = 600` set on pre-sync EtcdOpsTasks.

**Why**: Without an explicit TTL, the task could be garbage-collected before the statefulset component read its terminal state, causing `ensurePreSyncSnapshot` to create a new task and waste retry slots.

**Files**: `internal/component/statefulset/statefulset.go`

---

## Change Impact Summary

| Component | What | Gate-protected |
|-----------|------|----------------|
| Feature gate | New `UseEtcdSteward` alpha | N/A |
| EtcdMember CRD + component | New CRD, operator, sync ordering | Yes |
| StatefulSet builder | Args, volumes, probe, wrapper flag | Yes |
| Status reconciler | EtcdMember-based member status | Yes |
| Compaction controller | EtcdMember snapshot source, POST, HTTP | Yes |
| Peer TLS check | Skip lease annotation check | Yes |
| RBAC Role | `create` on leases, EtcdMember rules | No (additive) |
| Snapshot lease | Skip creation | Yes |
| Image vector | New `etcd-steward` image key | Yes (selection) |
| EtcdOpsTask HTTP client | Skip TLS for steward | Yes |
| EtcdOpsTask deletion predicate | Handles DeletionTimestamp events | No (bug fix) |
| Pre-sync task TTL | 600s TTL on EtcdOpsTask | No (bug fix) |
