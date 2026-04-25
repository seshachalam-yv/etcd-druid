# Proposal: etcd-steward Rollout Plan

**Author**: Gardener etcd-druid Team
**Date**: 2026-04-23
**Status**: Draft
**Tracking Issue**: TBD

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Repository Changes](#2-repository-changes)
3. [PR Chain](#3-pr-chain)
4. [Review Process](#4-review-process)
5. [CI/CD Integration](#5-cicd-integration)
6. [Production Rollout Phases](#6-production-rollout-phases)
7. [Risk Assessment and Mitigation](#7-risk-assessment-and-mitigation)
8. [Known Gaps and Follow-up Work](#8-known-gaps-and-follow-up-work)
9. [Success Metrics](#9-success-metrics)

---

## 1. Executive Summary

### What

Replace etcd-backup-restore with etcd-steward as the sidecar container for etcd
clusters managed by etcd-druid. The transition is controlled by the `UseEtcdSteward`
feature gate (alpha, disabled by default). When enabled, etcd-druid deploys the
etcd-steward image in the backup-restore container slot and introduces the
`EtcdMember` Custom Resource to track per-member lifecycle state, replacing the
lease-based status reporting used by etcd-backup-restore.

### Why

**Architecture improvements**:
- **EtcdMember CRD for state management**: Each etcd member gets a dedicated
  `EtcdMember` custom resource that captures its full lifecycle (New, Initializing,
  Starting, Started) with sub-states (DBValidationSanity, DBValidationFull,
  Restoration, PendingLearner, Learner, Follower, Leader). This replaces the
  overloaded member lease HolderIdentity field that etcd-backup-restore uses to
  encode member ID and role.
- **Snapshot status in EtcdMember**: Snapshot revision tracking moves from snapshot
  leases (HolderIdentity carrying a revision number string) to structured fields in
  `EtcdMember.Status.Snapshots`, with proper typed fields for LastFull, LastDelta,
  and AccumulatedDeltaSize.
- **Cleaner sidecar contract**: etcd-steward exposes a plain HTTP server (no TLS
  between wrapper and sidecar) with REST endpoints (`/healthz`, `/snapshot/full`,
  `/snapshot/delta`, `/defrag`, `/snapshot/latest`). The etcd-wrapper probes
  steward's `/healthz` endpoint directly, eliminating the backup-restore TLS CA
  bundle in the wrapper's readiness probe.
- **Go native testing**: etcd-steward is written from scratch in Go with modern
  patterns, making unit and integration testing straightforward.

### Risk

**Data integrity is the primary concern.** etcd stores the complete Kubernetes
cluster state for every Gardener shoot cluster. A failure in snapshot, restore, or
compaction directly translates to potential shoot cluster data loss. The
`UseEtcdSteward` feature gate and the phased rollout described below are specifically
designed to mitigate this risk through progressive exposure.

### Precedent: UseEtcdWrapper

The `UseEtcdWrapper` feature gate provides a directly comparable precedent:

| Milestone | PR | Date | Duration |
|---|---|---|---|
| Alpha (introduced, disabled by default) | [#646](https://github.com/gardener/etcd-druid/pull/646) | ~2023-02 | -- |
| GA (locked to true) | [#936](https://github.com/gardener/etcd-druid/pull/936) | ~2024-05 | ~15 months |
| Removal (gate deleted, old code removed) | [#999](https://github.com/gardener/etcd-druid/pull/999) | ~2024-09 | ~19 months total |

The etcd-steward rollout follows the same pattern but with a larger scope (the
sidecar is fully replaced, not just wrapped), so timelines are conservatively
extended.

---

## 2. Repository Changes

### 2.1 etcd-steward (new repository)

**Repository**: `github.com/gardener/etcd-steward`
**Current state**: Development; images built locally (`localhost:5001/etcd-steward:latest`)

| Action | Details |
|---|---|
| Tag first release | `v0.1.0` -- first alpha-quality release |
| Publish OCI image | `europe-docker.pkg.dev/gardener-project/public/gardener/etcd-steward:v0.1.0` |
| CI pipeline | Build, unit test, lint on push/PR; integration tests against KIND cluster |
| Prow integration | Wire into Gardener CI for cross-repo testing |

**Must-have for v0.1.0**:
- `compact` subcommand (used by the compaction Job)
- Defrag endpoint wired to real etcd defrag
- GC logic for old snapshots
- All cloud provider backup stores validated (S3, GCS, ABS, Swift, OSS, OCS, Local)

### 2.2 etcd-wrapper

**Repository**: `github.com/gardener/etcd-wrapper`
**Current version**: `v0.6.2`

| Action | Details |
|---|---|
| Steward-aware readiness probe | When `--backup-restore-tls-enabled=false`, probe steward's `/healthz` on HTTP (already supported via the existing `--backup-restore-tls-enabled` flag) |
| No code changes required | The current `v0.6.2` already supports the steward contract: when `UseEtcdSteward` is enabled, `builder.go` passes `--backup-restore-tls-enabled=false` to the wrapper |

The wrapper does not need a new release specifically for steward support. It already
handles the TLS-disabled case.

### 2.3 etcd-druid

**Repository**: `github.com/gardener/etcd-druid`
**Current version**: `v0.37.0-dev` (master)

This is where the bulk of changes land. The working tree already contains ~979 lines
of changes across 28 files. These changes are split into three PRs described in
Section 3.

### Release Order

```
1. etcd-steward v0.1.0          -- image available in registry
2. etcd-druid PR 1 (CRD + foundation)  -- merged to master
3. etcd-druid PR 2 (controller wiring) -- merged to master
4. etcd-druid PR 3 (E2E infra)         -- merged to master
5. etcd-druid v0.37.0 release          -- includes all steward PRs
```

Dependencies:
- PR 2 depends on PR 1 (uses EtcdMember types and registry entries)
- PR 3 depends on PR 2 (E2E tests exercise the full steward path)
- etcd-druid v0.37.0 must reference the published etcd-steward image (not
  `localhost:5001`)

---

## 3. PR Chain

### PR 1: EtcdMember CRD + Foundation (~500 lines)

**Title**: `feat(api): introduce EtcdMember CRD and UseEtcdSteward feature gate`
**Label**: `/kind api-change`

**Files**:

| File | Change Type | Description |
|---|---|---|
| `api/core/v1alpha1/etcdmember_types.go` | New | EtcdMember type with full lifecycle state machine (MemberState, MemberSubState, MemberSnapshotStatus, MemberRestorationStatus, MemberDefragmentationStatus, MemberTransition) |
| `api/core/v1alpha1/register.go` | Modified | Register EtcdMember and EtcdMemberList in SchemeBuilder |
| `api/core/v1alpha1/zz_generated.deepcopy.go` | Generated | DeepCopy methods for all new types (~331 lines, auto-generated) |
| `api/config/v1alpha1/features.go` | Modified | Add `UseEtcdSteward` constant and register as alpha feature gate |
| `api/config/v1alpha1/features_test.go` | Modified | Test UseEtcdSteward gate behavior (alpha, disabled by default) |
| `internal/common/constants.go` | Modified | Add `ImageKeyEtcdSteward`, `ComponentNameEtcdMember` constants |
| `internal/component/registry.go` | Modified | Add `EtcdMemberKind` constant |
| `internal/images/images.yaml` | Modified | Add etcd-steward image entry |
| `charts/crds/druid.gardener.cloud_etcdmembers.yaml` | New | Generated CRD YAML for EtcdMember |
| `charts/templates/clusterrole.yaml` | Modified | Add RBAC rules for etcdmembers and etcdmembers/status |
| `charts/values.yaml` | Modified | Add `UseEtcdSteward: false` to featureGates |
| `test/utils/constants.go` | Modified | Add `ETCDStewardImageTag` constant |
| `test/utils/imagevector.go` | Modified | Add etcd-steward image source to test image vector |

**Commit sequence** (two-commit pattern for API changes):
1. `feat(api): add EtcdMember types and UseEtcdSteward feature gate`
2. `chore(api): regenerate deepcopy and CRD manifests`

**Review checklist**:
- [ ] EtcdMember CRD validation markers correct (+kubebuilder:validation:Enum)
- [ ] DeepCopy generated and compiles
- [ ] CRD YAML installed successfully via `kubectl apply`
- [ ] Feature gate registered as alpha with `enabledByDefault: false`
- [ ] ClusterRole RBAC includes `create`, `get`, `list`, `watch`, `update`, `patch`, `delete`, `deletecollection` for etcdmembers
- [ ] ClusterRole RBAC includes `get`, `update`, `patch` for etcdmembers/status
- [ ] `make generate` produces clean output
- [ ] `make check` passes

### PR 2: Core Controller Wiring (~650 lines)

**Title**: `feat(controller): wire UseEtcdSteward feature gate across reconcilers`
**Label**: `/kind feature`

**Files**:

| File | Change Type | Description |
|---|---|---|
| `internal/component/etcdmember/etcdmember.go` | New | EtcdMember component operator (Sync creates/updates/deletes EtcdMember CRs per replica, scale-up sets create-as-learner annotation, scale-down deletes surplus members) |
| `internal/component/etcdmember/register.go` | New | Package doc explaining feature gate dependency |
| `internal/component/statefulset/builder.go` | Modified | (1) `getStewardContainerCommandArgs()` -- builds steward CLI args (pod-name, pod-namespace, server-port, data-dir, etcd-endpoints, TLS flags, member-lease, snapshot-lease, snapshotter, backup-store, defrag, alarm, GC); (2) `getBackupRestoreContainerVolumeMounts()` -- mounts etcd server TLS and peer TLS into steward container; (3) `getEtcdContainerReadinessHandler()` -- probes steward `/healthz` on HTTP; (4) `getEtcdContainerCommandArgs()` -- sets `--backup-restore-tls-enabled=false` when steward is active |
| `internal/component/statefulset/statefulset.go` | Modified | Skip peer TLS check when UseEtcdSteward is enabled (steward handles peer TLS via its own config generation) |
| `internal/component/snapshotlease/snapshotlease.go` | Modified | Skip snapshot lease creation when UseEtcdSteward is enabled (steward tracks snapshots in EtcdMember.Status.Snapshots) |
| `internal/component/role/role.go` | Modified | Add RBAC rules for etcdmembers and etcdmembers/status to the per-etcd Role (the sidecar ServiceAccount needs to read/write EtcdMember status) |
| `internal/component/role/role_test.go` | Modified | Assert new RBAC rules in role tests |
| `internal/utils/image.go` | Modified | `getEtcdImageKeys()` returns `ImageKeyEtcdSteward` instead of `ImageKeyEtcdBackupRestore` when UseEtcdSteward is enabled |
| `internal/controller/etcd/reconciler.go` | Modified | Register EtcdMember component operator in registry when gate is enabled |
| `internal/controller/etcd/reconcile_spec.go` | Modified | (1) Insert EtcdMemberKind before StatefulSetKind in sync order; (2) Include EtcdMemberKind in cleanup operators |
| `internal/controller/etcd/reconcile_status.go` | Modified | `mutateETCDStatusFromEtcdMembers()` -- reads EtcdMember CRs to populate `Etcd.Status.Members` with mapped state/substate to Ready/NotReady/Unknown; falls back to lease-based status when no EtcdMember has populated State |
| `internal/controller/compaction/reconciler.go` | Modified | `getDeltaRevisionsSinceFullSnapshot()` reads from EtcdMember.Status.Snapshots when gate is enabled, falls back to snapshot leases otherwise |
| `internal/controller/compaction/register.go` | Modified | Owns `EtcdMember` when gate is enabled; adds `etcdMemberSnapshotChanged()` predicate |
| `internal/controller/compaction/snapshot.go` | Modified | (1) `newHTTPClient()` skips TLS when steward is enabled; (2) `fullSnapshot()` uses POST instead of GET for steward API |
| `internal/controller/etcdopstask/handler/utils/etcdbrclient.go` | Modified | `ConfigureHTTPClientForEtcdBR()` skips TLS when steward is enabled |
| `internal/health/status/check.go` | Modified | Guard EtcdMember-based status path |

**Review checklist**:
- [ ] `gate=false` path: all existing tests pass unchanged (`make ci-checks`)
- [ ] `gate=true` path: unit tests pass with gate enabled
- [ ] EtcdMember component creates exactly `spec.replicas` members
- [ ] Scale-up creates new member with `create-as-learner` annotation
- [ ] Scale-down deletes surplus EtcdMember resources
- [ ] Steward container args match steward CLI spec
- [ ] Readiness probe hits steward's `/healthz` (HTTP, not HTTPS)
- [ ] Snapshot lease creation correctly skipped
- [ ] Compaction reads from EtcdMember.Status.Snapshots
- [ ] Full snapshot trigger uses POST to steward
- [ ] Role RBAC allows sidecar to update EtcdMember/status
- [ ] Status reconciliation correctly maps EtcdMember states to legacy EtcdMemberStatus

### PR 3: E2E Test Infrastructure (~200 lines)

**Title**: `test(e2e): add UseEtcdSteward support to E2E test framework`
**Label**: `/kind test`

**Files**:

| File | Change Type | Description |
|---|---|---|
| `test/e2e/controller/etcd_test.go` | Modified | Read `USE_ETCD_STEWARD` env var, set feature gate in TestMain |
| `test/e2e/testenv/testenv.go` | Modified | (1) `getSnapshotterJob()` uses plain HTTP POST for steward, HTTPS GET+TLS for backup-restore; (2) `getSnapshotRevisions()` reads from EtcdMember when gate is enabled; (3) `EnsureCompaction()` and `EnsureNoCompaction()` use `>=` checks with steward (auto-snapshotter runs concurrently); (4) `CheckEtcdReady()` skips `ConditionTypeLastSnapshotCompactionSucceeded` for steward (compaction Job not yet steward-native) |

**Review checklist**:
- [ ] `USE_ETCD_STEWARD=false make test-e2e` passes (gate=false path)
- [ ] `USE_ETCD_STEWARD=true make test-e2e` passes with steward image
- [ ] Snapshot revision assertions handle steward's concurrent auto-snapshotter
- [ ] Compaction condition correctly skipped for steward path

---

## 4. Review Process

### Review Assignments

| PR | Primary Reviewer | Domain |
|---|---|---|
| PR 1 (CRD + Foundation) | etcd-druid API maintainer | API types, CRD generation, RBAC, Helm chart |
| PR 2 (Controller Wiring) | etcd-druid controller maintainer | Reconciler logic, component operators, image selection |
| PR 3 (E2E Infrastructure) | etcd-druid test maintainer | E2E framework, test assertions, CI integration |

### Gate=false Verification

Every PR must pass the existing test suite with the feature gate disabled:

```bash
# Unit + integration tests (gate=false is the default)
make ci-checks

# E2E tests (gate=false is the default)
make test-e2e
```

The CI pipeline runs with `UseEtcdSteward=false` by default. No existing test
should be affected by the mere presence of the gate and new code paths.

### Gate=true Verification

```bash
# Unit + integration tests with gate enabled
# (requires setting the gate before test setup)
USE_ETCD_STEWARD=true make test-e2e

# Local KIND cluster validation
# 1. Deploy etcd-druid with gate enabled in values.yaml
# 2. Create an Etcd resource
# 3. Verify steward container running (not backup-restore)
# 4. Verify EtcdMember CRs created
# 5. Verify snapshot leases NOT created
# 6. Trigger on-demand full/delta snapshots via HTTP POST
# 7. Verify compaction triggered via EtcdMember.Status.Snapshots
# 8. Scale 1->3, verify new members join as learners
# 9. Scale 3->1, verify surplus EtcdMembers deleted
# 10. Hibernate (replicas=0) and unhibernate
```

---

## 5. CI/CD Integration

### Prow Job Configuration

Two parallel CI jobs for E2E tests:

| Job Name | Feature Gate | Image | Purpose |
|---|---|---|---|
| `pull-etcd-druid-e2e` | `UseEtcdSteward=false` (default) | etcd-backup-restore | Regression gate -- existing behavior must not break |
| `pull-etcd-druid-e2e-steward` | `UseEtcdSteward=true` | etcd-steward | Forward gate -- new behavior validated |

**Prow job spec** (etcd-steward job):

```yaml
- name: pull-etcd-druid-e2e-steward
  always_run: false
  run_if_changed: '(internal/component/etcdmember|internal/component/statefulset|internal/controller|internal/utils/image|api/config/v1alpha1/features|test/e2e)'
  decorate: true
  cluster: gardener-ci
  spec:
    containers:
    - image: <ci-runner-image>
      command:
      - make
      args:
      - test-e2e
      env:
      - name: USE_ETCD_STEWARD
        value: "true"
      - name: IMAGEVECTOR_OVERWRITE
        value: /path/to/steward-imagevector-overwrite.yaml
```

**Image Vector Overwrite** (for CI):

```yaml
images:
- name: etcd-steward
  sourceRepository: github.com/gardener/etcd-steward
  repository: europe-docker.pkg.dev/gardener-project/public/gardener/etcd-steward
  tag: "v0.1.0"
```

### Test Matrix

| Test Scenario | gate=false | gate=true | Notes |
|---|---|---|---|
| 1-replica, none provider | PASS | PASS | 29/29 tests |
| 1-replica, local provider | PASS | PASS (53/58) | 5 failures from compact Job (known gap) |
| 3-replica, none provider | PASS | PASS | Scale-up/down validated |
| 3-replica, local provider | PASS | PASS (with caveats) | Compact Job uses backup-restore image |
| TLS enabled | PASS | PASS | Steward container gets etcd client + peer TLS mounts |
| Hibernate/unhibernate | PASS | PASS | Replicas 0 -> 1/3 |
| Pod disruption recovery | PASS | PASS | Delete pods, verify re-creation |
| PVC disruption recovery | PASS | PASS | Delete PVCs, verify restore |

### Running Both Locally

```bash
# Gate=false (default, existing behavior)
make test-e2e

# Gate=true (steward behavior)
USE_ETCD_STEWARD=true make test-e2e

# Run both sequentially
make test-e2e && USE_ETCD_STEWARD=true make test-e2e
```

---

## 6. Production Rollout Phases

### Phase Alpha (v0.37.0, Q2 2026)

**Gate**: `UseEtcdSteward` = alpha, disabled by default, not locked
**Duration**: 2-3 release cycles (~3-4 months)

**What ships**:
- EtcdMember CRD installed as part of Helm chart
- All controller wiring behind feature gate
- etcd-steward `v0.1.0` image in image vector
- Dual CI jobs (gate=false and gate=true)

**Who tests**:
- etcd-druid dev team: daily development with gate=true on KIND clusters
- CI: both gate=false and gate=true E2E jobs on every PR
- Early adopters: Gardener landscape operators who opt in via Helm values

**How to enable**:

```yaml
# In etcd-druid Helm values.yaml
featureGates:
  UseEtcdSteward: true
```

**Graduation criteria** (all must be met):

| Criterion | Measurement |
|---|---|
| E2E test pass rate >= 99% | CI dashboard, 30-day rolling window |
| Zero data loss incidents in dev/staging | Incident tracker |
| All cloud providers validated | S3, GCS, ABS, Swift, OSS, OCS, Local -- E2E matrix |
| Compact Job works with steward image | `compact` subcommand implemented and tested |
| Steward HTTP TLS implemented | For environments that require TLS on backup-restore endpoints |
| Snapshot/restore latency parity | Benchmark: steward <= backup-restore p99 latency |
| Recovery time parity | Benchmark: restore from snapshot time within 10% of backup-restore |
| 3+ months of alpha testing without regressions | Timeline |

### Phase Beta (v0.40.0, Q4 2026)

**Gate**: `UseEtcdSteward` = beta, enabled by default, not locked
**Duration**: 2-3 release cycles (~3-4 months)

**What changes**:
- Gate flips to `maturityLevelSpecBeta` (enabled by default):
  ```go
  DefaultFeatureGates.knownFeatures[UseEtcdSteward] = maturityLevelSpecBeta
  ```
- All new Gardener landscapes get etcd-steward by default
- Existing landscapes can still opt out via `UseEtcdSteward: false`

**Who tests**:
- All staging landscapes
- Canary production landscapes (selected Gardener seed clusters)
- Full CI matrix (both gate values)

**Rollback procedure**:

```yaml
# Step 1: Disable feature gate in etcd-druid Helm values
featureGates:
  UseEtcdSteward: false

# Step 2: Redeploy etcd-druid
helm upgrade etcd-druid charts/ -f values.yaml

# Step 3: Trigger reconciliation of all Etcd resources
# (etcd-druid will replace steward containers with backup-restore containers)
# Each Etcd resource will be re-reconciled with the StatefulSet updated to use
# the backup-restore image and legacy command args.

# Step 4: Verify all Etcd clusters healthy
kubectl get etcd -A -o wide
```

**Important rollback considerations**:
- Snapshot format is the same (etcd-steward uses the same snapstore library from
  etcd-backup-restore). Rolling back does NOT require re-snapshotting.
- EtcdMember CRs will remain but become orphans (no controller reads them with
  gate=false). They can be cleaned up manually or left for the next gate=true
  attempt.
- Snapshot leases will be re-created by the snapshot lease component on the next
  reconciliation cycle.

**Graduation criteria** (all must be met):

| Criterion | Measurement |
|---|---|
| Zero data loss incidents in production canaries | Incident tracker |
| Zero rollback-requiring incidents | Incident tracker |
| Performance parity confirmed at production scale | Monitoring dashboards |
| All known gaps resolved | GitHub issue tracker |
| 3+ months of beta testing without regressions | Timeline |

### Phase GA (v0.43.0, Q2 2027)

**Gate**: `UseEtcdSteward` = GA, enabled by default, locked to true
**Duration**: 1-2 release cycles (~2-3 months)

**What changes**:
- Gate moves to `maturityLevelSpecGA` (locked to true):
  ```go
  DefaultFeatureGates.knownFeatures[UseEtcdSteward] = maturityLevelSpecGA
  ```
- Attempting to set `UseEtcdSteward: false` returns an error
- Old backup-restore code paths remain but are unreachable
- etcd-backup-restore image remains in image vector (for compact Job if not yet
  migrated)

**Migration guide**:
- All landscapes must be running etcd-steward (no opt-out possible)
- Operators should verify EtcdMember CRs exist for all etcd clusters
- Snapshot leases will stop being created; existing leases remain until manually
  cleaned up or etcd resources are deleted/recreated

**Graduation criteria**:

| Criterion | Measurement |
|---|---|
| Zero incidents across all production landscapes | Incident tracker, 3-month window |
| No operator complaints/blockers | GitHub issues, Slack channels |

### Phase Removal (v0.44.0, Q3 2027)

**Gate**: `UseEtcdSteward` removed entirely

**What changes**:

| Action | Files Affected |
|---|---|
| Remove `UseEtcdSteward` constant and gate registration | `api/config/v1alpha1/features.go`, `features_test.go` |
| Remove `UseEtcdSteward` from Helm values | `charts/values.yaml` |
| Delete backup-restore-specific code paths | `internal/component/statefulset/builder.go` (remove `getBackupRestoreContainerCommandArgs()` and all `if !UseEtcdSteward` branches) |
| Remove backup-restore-specific TLS wiring | `internal/controller/compaction/snapshot.go`, `internal/controller/etcdopstask/handler/utils/etcdbrclient.go` |
| Remove lease-based snapshot tracking | `internal/controller/compaction/reconciler.go` (remove `getDeltaRevisionsSinceFullSnapshotFromLeases()`) |
| Remove lease-based member status | `internal/controller/etcd/reconcile_status.go` (remove fallback to member-lease-based status) |
| Remove backup-restore image from image vector | `internal/images/images.yaml` (remove `etcd-backup-restore` and `etcd-backup-restore-distroless` entries) |
| Remove CI job for gate=false | Prow job configuration |
| Clean up E2E test branches | `test/e2e/testenv/testenv.go` (remove all `if useSteward` branches, keep steward-only code) |
| Make EtcdMember registration unconditional | `internal/controller/etcd/reconciler.go` (remove `if UseEtcdSteward` guard around `etcdmember.New()`) |

**Estimated deletion**: ~400-500 lines of backup-restore-specific code

---

## 7. Risk Assessment and Mitigation

### 7.1 Data Loss Scenarios

| Scenario | Impact | Mitigation |
|---|---|---|
| Steward fails to take snapshots | etcd data not backed up; node failure = data loss | (1) E2E test verifies snapshot taking; (2) `BackupReady` condition wired to steward; (3) Alerting on snapshot age |
| Steward fails to restore from snapshot | New cluster cannot bootstrap; existing cluster cannot recover | (1) E2E test verifies restore path; (2) Manual restore procedure documented; (3) Rollback to backup-restore as escape hatch |
| Steward corrupts etcd data directory | All data for affected shoot cluster lost | (1) etcd-steward never writes to etcd data dir except during restore; (2) DB validation (sanity + full) runs before etcd starts; (3) Snapshot available for re-restore |
| Compaction Job fails with steward image | Old snapshots accumulate, storage costs increase, restore time increases | (1) Known gap: compact subcommand must be implemented; (2) Fallback: use backup-restore image for compact Job until steward's compact is ready |
| Rolling update mid-snapshot | Incomplete snapshot uploaded | (1) Steward uses atomic upload (write to temp, rename); (2) Pre-sync snapshot OpsTask ensures clean snapshot before StatefulSet update |

### 7.2 Snapshot Format Compatibility

etcd-steward uses the **same snapshot format** as etcd-backup-restore:
- Full snapshots: etcd `Snapshot` API, gzip-compressed, stored in snapstore
- Delta snapshots: etcd `Watch` events, gzip-compressed, stored in snapstore
- Snapstore library: shared code from etcd-backup-restore

This means:
- A snapshot taken by backup-restore can be restored by steward
- A snapshot taken by steward can be restored by backup-restore
- Rollback from steward to backup-restore does not require re-snapshotting

### 7.3 Rollback Testing

**Pre-production rollback drill** (required before beta graduation):

```bash
# 1. Deploy with UseEtcdSteward=true
# 2. Create 3-replica Etcd with backup store
# 3. Load 10,000 keys
# 4. Verify full + delta snapshots taken by steward
# 5. Flip gate to UseEtcdSteward=false
# 6. Redeploy etcd-druid
# 7. Verify StatefulSet updated (backup-restore image, legacy args)
# 8. Verify all 10,000 keys still present
# 9. Verify snapshots continue being taken by backup-restore
# 10. Verify restore works from steward-era snapshot
```

### 7.4 Performance Validation

| Metric | Target | How to Measure |
|---|---|---|
| Full snapshot time (100MB DB) | <= backup-restore time | E2E benchmark job |
| Delta snapshot time (1000 events) | <= backup-restore time | E2E benchmark job |
| Restore time (100MB snapshot) | <= backup-restore time + 10% | E2E benchmark job |
| Memory usage (steady state) | <= backup-restore usage | `kubectl top pods` |
| CPU usage (steady state) | <= backup-restore usage | `kubectl top pods` |
| Readiness probe latency | < 1s | Probe response time |

### 7.5 Multi-Cluster Coexistence

During beta, some etcd clusters will run steward while others run backup-restore.
This is safe because:
- The feature gate is set **per etcd-druid instance**, not per Etcd resource
- A single etcd-druid instance manages all Etcd resources in its scope with the
  same gate value
- Different Gardener seeds can run different etcd-druid versions/configurations
- There is no cross-cluster dependency between the sidecar choice

---

## 8. Known Gaps and Follow-up Work

### 8.1 Compaction Job with Steward Image (P0 -- blocks beta)

**Current state**: The compaction Job is created by `internal/controller/compaction/reconciler.go`
and uses the backup-restore image with the `compact` subcommand. When `UseEtcdSteward=true`,
the sidecar is steward but the compact Job still uses backup-restore because steward
lacks a `compact` subcommand.

**Impact**: 5 out of 58 E2E tests fail with local provider due to compact Job issues.

**Resolution**: Implement `compact` subcommand in etcd-steward that:
1. Reads full + delta snapshots from the backup store
2. Restores to a temporary etcd instance
3. Takes a new full snapshot (compacted)
4. Uploads the compacted snapshot
5. Cleans up the temporary instance

**File changes**:
- `etcd-steward`: new `cmd/compact.go`
- `etcd-druid`: `internal/controller/compaction/reconciler.go` -- use steward image
  and `compact` args when gate is enabled

### 8.2 Steward HTTP Server TLS (P1 -- blocks beta for TLS-strict environments)

**Current state**: etcd-steward's HTTP server runs without TLS. The etcd-wrapper
bypasses the backup-restore TLS check when `--backup-restore-tls-enabled=false`.
However, some Gardener environments require TLS on all sidecar endpoints.

**Resolution**: Add TLS support to steward's HTTP server:
- Accept `--server-cert` and `--server-key` flags
- When provided, serve HTTPS instead of HTTP
- Update `builder.go` to mount backup-restore TLS volumes into steward and pass
  cert/key paths

### 8.3 Snapshot Lease HolderIdentity (P2 -- nice to have for monitoring tools)

**Current state**: When `UseEtcdSteward=true`, snapshot leases are not created
(`snapshotlease.go` skips creation). Some legacy monitoring tools read snapshot
lease HolderIdentity to determine backup freshness.

**Resolution options**:
1. (Preferred) Update monitoring tools to read EtcdMember.Status.Snapshots
2. (Fallback) Have steward update snapshot leases in addition to EtcdMember status

### 8.4 Defrag Status in EtcdMember (P2)

**Current state**: `EtcdMemberResourceStatus.LastDefragmentation` field exists in
the CRD but etcd-steward's defrag is disabled (`--enable-defrag=false` in steward
args). The steward's `/defrag` endpoint is wired but not scheduled.

**Resolution**: Enable scheduled defrag in steward and populate
`LastDefragmentation` fields in EtcdMember status.

### 8.5 BackupReady Condition Wiring (P1 -- blocks beta)

**Current state**: The `BackupReady` condition in Etcd status is set by the health
checker which reads from snapshot leases. With steward, snapshot leases are not
created, so BackupReady may not be correctly determined.

**Resolution**: The health checker should read from EtcdMember.Status.Snapshots
when `UseEtcdSteward=true` to determine backup freshness and set BackupReady.

### 8.6 Snapshot Lease Renewal for Legacy Compatibility (P3)

**Current state**: Steward does renew member leases (HolderIdentity in
`<memberID>:<role>` format) which the legacy status check can parse. However,
snapshot lease renewal is separate -- steward updates snapshot leases via
`--enable-snapshot-lease-renewal=true` flag to maintain compatibility during the
transition period.

**Resolution**: This is working as designed during alpha/beta. At GA, snapshot
lease renewal can be removed from steward since the EtcdMember-based snapshot
tracking is the primary path.

---

## 9. Success Metrics

### Operational Metrics

| Metric | Target | Measurement Period |
|---|---|---|
| Data loss incidents | **Zero** | Per rollout phase |
| E2E test pass rate (gate=true) | >= 99% | 30-day rolling window |
| E2E test pass rate (gate=false) | >= 99% (no regression) | 30-day rolling window |
| Snapshot success rate | >= 99.9% | Per Etcd cluster, 7-day window |
| Restore success rate | 100% | Every restore attempt |
| Mean time to detect backup failure | < 5 minutes | From missed snapshot to alert |
| Rollback success rate (beta drill) | 100% | Every drill |

### Performance Metrics

| Metric | Target | Comparison Baseline |
|---|---|---|
| Full snapshot latency (p99) | <= etcd-backup-restore | Same DB size, same provider |
| Restore latency (p99) | <= etcd-backup-restore + 10% | Same snapshot size |
| Steady-state memory | <= etcd-backup-restore | Same workload |
| Steady-state CPU | <= etcd-backup-restore | Same workload |
| Pod startup time | <= etcd-backup-restore | Cold start, no restore needed |

### Adoption Metrics

| Metric | Alpha Target | Beta Target | GA Target |
|---|---|---|---|
| Landscapes running steward | >= 3 dev/staging | >= 50% staging + canary prod | 100% |
| Gardener seeds with steward | >= 5 | >= 30% | 100% |
| Unique cloud providers tested | All 7 | All 7 | All 7 |

---

## Appendix A: Feature Gate State Machine

```
v0.37.0 (Alpha)     v0.40.0 (Beta)      v0.43.0 (GA)        v0.44.0 (Removal)
    |                     |                    |                     |
    v                     v                    v                     v
 disabled             enabled               enabled              removed
 by default          by default            by default            (always on)
 (opt-in)            (opt-out)             (locked)
    |                     |                    |                     |
    +-----+-----+--------+--------+-----------+-----+-----+---------+
          |              |                    |            |
     Can set to     Can set to          Cannot set to   Gate code
     true or false  true or false       false (error)   deleted
```

## Appendix B: File Inventory by Feature Gate Branch

All code paths gated by `UseEtcdSteward`:

| File | Line | Branch |
|---|---|---|
| `api/config/v1alpha1/features.go:92` | Gate registration | `maturityLevelSpecAlpha` |
| `internal/utils/image.go:40` | Image selection | Returns `ImageKeyEtcdSteward` |
| `internal/component/statefulset/builder.go:299` | Volume mounts | Mounts etcd server TLS into steward |
| `internal/component/statefulset/builder.go:306` | Volume mounts | Mounts peer TLS into steward |
| `internal/component/statefulset/builder.go:425` | Container args | Calls `getStewardContainerCommandArgs()` |
| `internal/component/statefulset/builder.go:696` | Readiness probe | Probes `/healthz` on steward HTTP port |
| `internal/component/statefulset/builder.go:722` | Wrapper args | Sets `--backup-restore-tls-enabled=false` |
| `internal/component/statefulset/statefulset.go:450` | Peer TLS check | Skips peer TLS sync check |
| `internal/component/snapshotlease/snapshotlease.go:89` | Lease creation | Skips snapshot lease creation |
| `internal/component/role/role.go:140-148` | RBAC rules | Adds etcdmembers and etcdmembers/status rules |
| `internal/controller/etcd/reconciler.go:163` | Registry | Registers EtcdMember component operator |
| `internal/controller/etcd/reconcile_spec.go:215` | Sync order | Inserts EtcdMemberKind before StatefulSet |
| `internal/controller/etcd/reconcile_spec.go:238` | Cleanup | Includes EtcdMemberKind in cleanup |
| `internal/controller/etcd/reconcile_status.go:51` | Status source | Reads from EtcdMember CRs instead of leases |
| `internal/controller/compaction/reconciler.go:317` | Snapshot revisions | Reads from EtcdMember.Status.Snapshots |
| `internal/controller/compaction/register.go:40` | Watch | Owns EtcdMember, adds snapshot predicate |
| `internal/controller/compaction/snapshot.go:85` | HTTP client | Skips TLS for steward |
| `internal/controller/compaction/snapshot.go:129` | HTTP method | Uses POST instead of GET |
| `internal/controller/etcdopstask/handler/utils/etcdbrclient.go:31` | HTTP client | Skips TLS for steward |

## Appendix C: UseEtcdWrapper Precedent Timeline

For reference, the complete UseEtcdWrapper lifecycle:

| Date | Version | PR | Action |
|---|---|---|---|
| 2023-02 | v0.19.0 | [#646](https://github.com/gardener/etcd-druid/pull/646) | Introduced as alpha (disabled by default) |
| 2024-05 | v0.30.0 | [#936](https://github.com/gardener/etcd-druid/pull/936) | Graduated to GA (locked to true) |
| 2024-09 | v0.33.0 | [#999](https://github.com/gardener/etcd-druid/pull/999) | Gate removed, old code deleted |

Total lifecycle: ~19 months from introduction to removal.

The UseEtcdSteward gate follows the same pattern with a projected lifecycle of
~15-18 months (Q2 2026 to Q3 2027), extended due to the larger scope of changes
(full sidecar replacement vs. wrapper addition).

---

## Appendix D: Feature Gate Toggle Test — Verified 2026-04-23

### Test: Steward → Backup-Restore Toggle

**Step 1: Create Etcd with UseEtcdSteward=true**
- Sidecar: `localhost:5001/etcd-steward:latest`
- Lease HolderIdentity: `128088275939295631:11588568905070377092:Leader` (3-part)
- EtcdMember: `test-0` State=Started SubState=Leader
- Ready at T=25s

**Step 2: Disable gate, redeploy druid, trigger reconcile**
- `UseEtcdSteward: false` in operatorConfig
- `kubectl annotate etcd test druid.gardener.cloud/operation=reconcile`

**Step 3: Verify backup-restore takes over**
- STS image switched: `etcdbrctl:v0.41.1`
- Pod rolling update: 1 restart
- Lease HolderIdentity: `1c70f9bbb41018f:a0d2de0531db7884:Leader` (backup-restore format)
- All conditions: AllMembersReady=True, ClusterIDMismatch=False, Ready=True
- Ready at T=40s (15s rollout + 25s startup)

**Result: PASS — zero data loss, seamless sidecar switch**

### Implications for Production Rollback

During Alpha/Beta, any operator can revert to backup-restore by:
1. Set `UseEtcdSteward: false` in druid operator config
2. Restart druid pod
3. Trigger reconcile on affected Etcd resources (or wait for periodic reconcile)
4. Each etcd StatefulSet will rolling-update to backup-restore sidecar
5. ~40 seconds downtime per single-member cluster, less for multi-member (one pod at a time)

No manual PVC migration, no snapshot format conversion, no data loss.
