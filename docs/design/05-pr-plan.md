# Spec: PR Plan for UseEtcdSteward

## Status

Draft -- 2026-04-24

## Principles

1. Each PR is independently mergeable and releasable.
2. When `UseEtcdSteward=false` (the default), behavior is IDENTICAL to master.
3. No PR breaks existing e2e tests.
4. Smaller PRs = faster review = faster merge.

## Cross-Repo Release Order

1. **gardener/etcd-steward** -> `v0.1.0` (new repo, initial release)
2. **gardener/etcd-wrapper** -> `v0.7.1` (steward-compat: wrapper must speak HTTP to steward instead of HTTPS to backup-restore)
3. **gardener/etcd-druid** -> PRs 1-6 (this document)

Why this order: Druid's `images.yaml` must reference tagged releases, never `latest`. Following the UseEtcdWrapper precedent (PR #646) where dependent repos were released before the druid PR merged.

## Cleanup Before PRs

Before opening PR 1 the working tree must be sanitized:

| Item | Current State | Required State |
|------|---------------|----------------|
| `internal/images/images.yaml` `etcd-steward` | `repository: localhost:5001/etcd-steward`, `tag: "latest"` | `repository: europe-docker.pkg.dev/gardener-project/releases/gardener/etcd-steward`, `tag: "v0.1.0"` |
| `charts/values.yaml` `UseEtcdSteward` | `false` | `false` (already correct) |
| `internal/component/etcdmember/etcdmember/` (nested duplicate) | exists | delete -- only `internal/component/etcdmember/etcdmember.go` + `register.go` should remain |
| Untracked dev files (`.worktrees/`, `poc/`, `manifests/`, `*.log`, etc.) | present | not committed (already gitignored or untracked) |

## PR Chain

### PR 1: Feature Gate + Constants + Image Vector

**Summary:** Register the `UseEtcdSteward` feature gate at alpha maturity, add image key / component constants, wire the etcd-steward image into the image vector and test helpers.

**Files (8 files, ~35 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `api/config/v1alpha1/features.go` | +5 | `UseEtcdSteward` constant and `maturityLevelSpecAlpha` registration |
| `api/config/v1alpha1/features_test.go` | +7/-2 | Per-subtest feature gate to avoid race on global state; add `UseEtcdSteward` to known features |
| `internal/common/constants.go` | +4 | `ImageKeyEtcdSteward`, `ComponentNameEtcdMember` |
| `internal/images/images.yaml` | +4 | `etcd-steward` image entry (tagged release, proper registry) |
| `internal/utils/image.go` | +7 | Return steward image key when `UseEtcdSteward` is enabled |
| `charts/values.yaml` | +6/-4 | `UseEtcdSteward: false` in both `featureGates` blocks |
| `test/utils/constants.go` | +2 | `ETCDStewardImageTag` |
| `test/utils/imagevector.go` | +5 | Append `etcd-steward` `ImageSource` to test vector |

**Risk:** Zero -- additive constants and a disabled-by-default feature gate.
**Review time:** ~15 min.
**Test:**
```bash
make test-unit
```

---

### PR 2: EtcdMember CRD Types + DeepCopy + Scheme Registration

**Summary:** Introduce the `EtcdMember` and `EtcdMemberList` API types, generated deepcopy functions, and scheme registration. Types only -- no controller or component instantiates them yet.

**Files (3 files, ~676 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `api/core/v1alpha1/etcdmember_types.go` | +343 (new) | `EtcdMember` spec/status types, `MemberState*`, `MemberSubState*` constants, snapshot info structs |
| `api/core/v1alpha1/zz_generated.deepcopy.go` | +331 | Auto-generated deepcopy for all EtcdMember types |
| `api/core/v1alpha1/register.go` | +2 | `&EtcdMember{}`, `&EtcdMemberList{}` added to `addKnownTypes` |

**Risk:** Zero -- types only, not instantiated anywhere.
**Review time:** ~30 min (verify types match DEP-04 design, check kubebuilder markers).
**Test:**
```bash
make test-unit
cd api && make generate  # verify deepcopy regenerates cleanly
```

---

### PR 3: RBAC Additions

**Summary:** Grant druid's ClusterRole and the per-etcd Role the permissions needed to manage EtcdMember resources. Also adds `create` verb to leases (steward creates its own member lease).

**Files (4 files, ~55 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `charts/templates/clusterrole.yaml` | +21 | `etcdmembers` and `etcdmembers/status` CRUD rules |
| `internal/component/role/role.go` | +11/-1 | Add `create` to lease verbs; add `etcdmembers` + `etcdmembers/status` policy rules |
| `internal/component/role/role_test.go` | +11/-1 | Update role matcher expectations |
| `internal/component/registry.go` | +2 | `EtcdMemberKind` constant |

**Risk:** Low -- purely additive RBAC permissions, no removal.
**Review time:** ~15 min.
**Test:**
```bash
make test-unit
```

---

### PR 4: StatefulSet Builder + Image Selection

**Summary:** The most critical PR. Behind the `UseEtcdSteward` feature gate, the StatefulSet builder: (a) injects steward-specific container args via `getStewardContainerCommandArgs()`, (b) mounts TLS volumes into the sidecar, (c) switches the readiness probe to HTTP `/healthz`, (d) disables backup-restore TLS for the etcd-wrapper args. Also handles the snapshot lease skip and preSyncTask TTL.

**Files (4 files, ~135 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `internal/component/statefulset/builder.go` | +108/-2 | `getStewardContainerCommandArgs()`, TLS volume mounts, readiness probe, wrapper TLS flag |
| `internal/component/statefulset/statefulset.go` | +11/-2 | `preSyncTaskTTLSeconds` constant, `TTLSecondsAfterFinished` on pre-sync task, steward gate check in `handleTLSChanges` |
| `internal/component/snapshotlease/snapshotlease.go` | +8 | Skip snapshot lease creation when `UseEtcdSteward` is enabled |
| `internal/utils/image.go` | (already in PR 1) | -- |

**Risk:** Medium -- gated behind `UseEtcdSteward=false` default, but touches the StatefulSet builder which is the most sensitive reconciliation path.
**Review time:** ~1 hour.
**Test:**
```bash
make test-unit
# e2e with gate OFF (default) -- must be identical to master
make test-e2e
# e2e with gate ON
USE_ETCD_STEWARD=true make test-e2e
```

---

### PR 5: Controller Wiring + Status Reconciliation

**Summary:** Wire the EtcdMember component into the operator registry, sync order, and cleanup list. Add steward-aware status reconciliation (`mutateETCDStatusFromEtcdMembers`), compaction controller changes (read revisions from EtcdMember status, POST instead of GET for snapshots, predicate for EtcdMember snapshot changes), and EtcdOpsTask predicate for force-delete.

**Files (10 files, ~275 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `internal/controller/etcd/reconciler.go` | +4 | Import and register `etcdmember.New` when gate is enabled |
| `internal/controller/etcd/reconcile_spec.go` | +14/-2 | Insert `EtcdMemberKind` before `StatefulSetKind` in sync order; add to cleanup list |
| `internal/controller/etcd/reconcile_status.go` | +118 | `mutateETCDStatusFromEtcdMembers()` with fallback to lease-based path |
| `internal/controller/compaction/reconciler.go` | +48 | `getDeltaRevisionsSinceFullSnapshotFromEtcdMembers()` |
| `internal/controller/compaction/register.go` | +48/-4 | `etcdMemberSnapshotChanged()` predicate; `Owns(&EtcdMember{})` when gate enabled |
| `internal/controller/compaction/snapshot.go` | +9/-3 | POST instead of GET for full snapshot; skip TLS for steward |
| `internal/controller/etcdopstask/handler/utils/etcdbrclient.go` | +2/-1 | Skip backup TLS config when steward enabled |
| `internal/controller/etcdopstask/register.go` | +21/-1 | `markedForDeletionPredicate()` for force-delete events |
| `internal/health/status/check.go` | +7 | Export `ExecuteConditionChecks()` for steward status path |
| `internal/component/etcdmember/etcdmember.go` | +224 (new) | EtcdMember component: Sync creates/updates EtcdMember CRs per replica |
| `internal/component/etcdmember/register.go` | +27 (new) | Component registration |

**Risk:** Medium -- gated behind `UseEtcdSteward=false` default. Status reconciliation has a fallback path for the transition period.
**Review time:** ~1 hour.
**Test:**
```bash
make test-unit
make test-integration
# e2e with gate ON
USE_ETCD_STEWARD=true make test-e2e
```

---

### PR 6: E2E Test Infrastructure + Documentation

**Summary:** E2E test changes to support `USE_ETCD_STEWARD=true` env var, steward-aware snapshot jobs (POST vs GET, no TLS), pod filtering to exclude job pods, and condition skip for compaction. Also includes feature gate documentation and architecture docs.

**Files (8 files, ~175 lines added):**

| File | Lines +/- | Description |
|------|-----------|-------------|
| `test/e2e/controller/etcd_test.go` | +8 | Read `USE_ETCD_STEWARD` env var, set feature gate |
| `test/e2e/testenv/testenv.go` | +155/-30 | Steward-aware `getSnapshotterJob()` (POST, no TLS), pod filtering, condition skip |
| `docs/deployment/feature-gates.md` | +4/-2 | Add `UseEtcdSteward` to alpha feature gate table |
| `docs/deployment/use-etcd-steward.md` | +201 (new) | Migration guide: enabling, verifying, rolling back |
| `docs/concepts/etcdmember-implementation.md` | +231 (new) | EtcdMember design and implementation details |
| `docs/concepts/use-etcd-steward-architecture.md` | +263 (new) | Architecture overview |
| `docs/README.md` | +3 | Links to new docs |
| `mkdocs.yml` | +3 | Navigation entries |

**Risk:** Zero -- test-only and documentation changes.
**Review time:** ~30 min.
**Test:**
```bash
USE_ETCD_STEWARD=true make test-e2e
```

## PR Description Template

Each PR should use the following description format:

```markdown
## What

<1-2 sentence summary of the change>

## Why

Part of the UseEtcdSteward feature (gardener/etcd-druid#XXXX).
PR N of 6 in the incremental delivery chain.

## How

- <bullet list of key implementation details>
- Feature-gated behind `UseEtcdSteward` (alpha, default=false)
- When gate is off, behavior is identical to master

## Dependencies

- Depends on: PR N-1 (if applicable)
- Required before: PR N+1 (if applicable)

## Test Plan

- [ ] `make test-unit` passes
- [ ] `make test-integration` passes (if applicable)
- [ ] e2e with `UseEtcdSteward=false` is identical to master
- [ ] e2e with `USE_ETCD_STEWARD=true` passes (if applicable)

/kind feature
/area etcd-steward
```

## Test Matrix Per PR

| PR | `make test-unit` | `make test-integration` | e2e gate=false | e2e gate=true |
|----|:---:|:---:|:---:|:---:|
| PR 1: Feature Gate + Constants | Y | - | - | - |
| PR 2: EtcdMember CRD Types | Y | - | - | - |
| PR 3: RBAC Additions | Y | - | - | - |
| PR 4: StatefulSet Builder | Y | - | Y | Y |
| PR 5: Controller Wiring | Y | Y | Y | Y |
| PR 6: E2E + Docs | Y | - | - | Y |

Legend: **Y** = required, **-** = not applicable for this PR.

## Total Change Summary

| Category | Files | Lines Added | Lines Removed |
|----------|-------|-------------|---------------|
| API types + config | 6 | ~700 | ~2 |
| Constants + image vector | 4 | ~15 | 0 |
| RBAC | 4 | ~45 | ~2 |
| StatefulSet builder | 3 | ~127 | ~4 |
| Controller wiring + status | 12 | ~520 | ~12 |
| E2E tests + docs | 8 | ~665 | ~32 |
| **Total** | **31** | **~2,072** | **~52** |

## Rollback Plan

At any point, setting `UseEtcdSteward: false` in `values.yaml` (the default) reverts all behavior to the pre-steward code path. No data migration is required. The EtcdMember CRDs remain installed but are inert when the gate is off.

To fully uninstall:
1. Set `UseEtcdSteward: false` and redeploy.
2. Delete orphaned EtcdMember CRs: `kubectl delete etcdmembers --all -A`.
3. (Optional) Remove the EtcdMember CRD if no longer needed.
