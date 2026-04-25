# UseEtcdSteward: Rollout Plan

## Overview

Replace etcd-backup-restore sidecar with etcd-steward behind the `UseEtcdSteward` alpha feature gate. The gate MUST work in both directions:
- **Disabled (default)**: 100% backup-restore behavior, zero changes
- **Enabled**: etcd-steward sidecar with EtcdMember-based status

## Phase 0: Pre-requisites (before any druid PR)

### Repo releases needed first
1. **gardener/etcd-steward** → tag `v0.1.0` with all packages
2. **gardener/etcd-wrapper** → tag `v0.7.1` with steward-compat (POST /embedded-etcd support)

### Why releases first?
Following the UseEtcdWrapper pattern (PR #646): dependent repos were released BEFORE the druid PR merged. The druid's `images.yaml` must point to tagged releases, never `latest`.

## Phase 1: Foundation PR (zero risk, merge fast)

### PR 1: "Introduce UseEtcdSteward feature gate and EtcdMember CRD"

**Scope:**
- Feature gate definition (alpha, disabled by default)
- EtcdMember CRD types + deepcopy + generated client
- Image vector entry for etcd-steward
- Constants (ImageKeyEtcdSteward, ComponentNameEtcdMember)
- ClusterRole RBAC for etcdmembers
- Helm CRD files
- Test constants

**Lines:** ~500 (mostly generated)
**Risk:** Zero — gate defaults to false, purely additive
**Review focus:** CRD schema matches DEP-04 proposal
**Label:** `/kind api-change`

**Compatibility:** Both paths work. Gate disabled = no EtcdMember resources created, no steward image used.

## Phase 2: Core Wiring PR (the behavioral change)

### PR 2: "Wire UseEtcdSteward into druid controllers"

**Scope:**
- StatefulSet builder: steward container args, volumes, probes, TLS config
- Status reconciler: read EtcdMember.Status with lease fallback
- Compaction controller: POST /snapshot/full, HTTP for steward, EtcdMember-based revision reads
- Peer TLS annotation bypass
- Snapshot lease skip when steward enabled
- EtcdMember component (Sync creates CRs, TriggerDelete cleans up)

**Lines:** ~650 production code
**Risk:** Medium — all gated by `if UseEtcdSteward.IsEnabled()`
**Review focus:** Each `if UseEtcdSteward` branch, ensure else path is unchanged

**Compatibility test matrix:**

| Test | UseEtcdSteward=false | UseEtcdSteward=true |
|------|---------------------|---------------------|
| TestBasic (6) | Must pass (backup-restore) | Must pass (steward) |
| TestRecovery (7) | Must pass | Must pass |
| TestScaleOut (5) | Must pass | Must pass |
| TestClusterUpdate (4) | Must pass | Must pass |
| TestTLSAndLabelUpdates (6) | Must pass | Must pass |
| Unit tests | Must pass | Must pass |

**Review strategy:** "Review by area"
1. Reviewer A: StatefulSet builder changes
2. Reviewer B: Controller changes (compaction, status, etcdopstask)
3. Reviewer C: Overall architecture + test coverage

## Phase 3: E2E Test PR

### PR 3: "E2E test infrastructure for UseEtcdSteward"

**Scope:**
- `USE_ETCD_STEWARD` env var in TestMain
- Snapshot revision reads from EtcdMember
- HTTP curl for steward snapshots
- Job pod filter in getEtcdPods
- Compaction condition skip for steward

**Lines:** ~200
**Risk:** Low — test-only
**Label:** `/kind test`

## Phase 4: CI Integration

### PR 4: "Add UseEtcdSteward e2e CI job"

Following the UseEtcdWrapper CI pattern (ci-infra PR #798):
- New prow job: `ci-e2e-kind-steward`
- Runs with `USE_ETCD_STEWARD=true`
- Tests both `none` and `local` providers
- Separate from existing e2e job (which runs with gate disabled)

## Production Rollout Phases

### Alpha (v0.37, 3-6 months)

**Gate:** `UseEtcdSteward` = alpha, disabled by default

**Who tests:**
- Dev team: manual testing with `UseEtcdSteward=true` in dev landscapes
- CI: dedicated prow job with gate enabled
- No production clusters

**Graduation criteria to Beta:**
- 29/29 `none` provider e2e tests pass
- All `local` provider basic/scaleout/TLS/cluster-update tests pass
- Compaction Job with steward image works (follow-up fix)
- 3 months of CI stability with no flakes
- Snapshot format compatibility proven (steward reads backup-restore snapshots, vice versa)

### Beta (v0.40+, 6-9 months)

**Gate:** `UseEtcdSteward` = beta, enabled by default

**Who tests:**
- All dev/staging Gardener landscapes
- First canary production landscape with low-risk etcd clusters

**Backward compat requirement:**
- Any landscape operator can set `UseEtcdSteward=false` to revert to backup-restore
- Existing snapshots must be readable by both sidecars
- Rolling back from steward to backup-restore must not lose data

**Graduation criteria to GA:**
- 6 months of production use without data loss
- Performance parity (snapshot latency, restore time, defrag duration)
- All compaction tests pass
- Backup/restore cycle validated under load (100+ keys/sec)

### GA (v0.43+, 2-3 months)

**Gate:** `UseEtcdSteward` = GA, locked to true

**What happens:**
- Old backup-restore code paths marked as deprecated
- CI job for disabled gate removed
- Migration guide published

### Removal (v0.44+)

**What happens:**
- Feature gate constant removed
- All `if UseEtcdSteward` branches removed (steward path becomes the only path)
- etcd-backup-restore image key removed from images.yaml
- ~300 lines of old code deleted

## Risk Mitigation

### Data Safety
1. **Snapshot format:** etcd-steward uses identical snapstore format (same SnapInfo struct, same naming convention)
2. **Rollback test:** Explicitly test: enable gate → take snapshots → disable gate → backup-restore resumes from steward's snapshots
3. **No dual-write:** Only one sidecar runs per pod, determined at STS creation time by the gate

### Feature Gate Compatibility Matrix

| Scenario | Expected Behavior |
|----------|-------------------|
| Gate disabled, fresh cluster | Uses backup-restore, standard behavior |
| Gate enabled, fresh cluster | Uses steward, EtcdMember status |
| Gate disabled → enabled (existing cluster) | Next STS reconcile switches sidecar image, rolling restart |
| Gate enabled → disabled (rollback) | Next STS reconcile switches back to backup-restore, rolling restart |
| Gate enabled, steward takes snapshots, then disabled | Backup-restore reads steward's snapshots (same format) |

### Review Checklist for Each PR

- [ ] Every `if UseEtcdSteward` branch has an `else` that preserves old behavior
- [ ] No changes to files outside the gate (except RBAC which is additive)
- [ ] Unit tests cover both gate=true and gate=false paths
- [ ] `make test-unit` passes
- [ ] `make test-integration` passes
- [ ] E2E with gate disabled: all existing tests pass
- [ ] E2E with gate enabled: all steward tests pass

## Timeline Summary

```
2026-Q2: PR 1+2+3 merge → Alpha in v0.37
2026-Q3: CI stability proven, compaction Job fixed
2026-Q4: Beta promotion → v0.40
2027-Q1: Production validation in canary landscapes
2027-Q2: GA → v0.43
2027-Q3: Removal → v0.44
```

Total: ~15 months (consistent with UseEtcdWrapper's 19 months)
