# etcd-steward Development Status

**Date**: 2026-04-25
**Author**: Seshachalam Yerasala Venkata
**Status**: Implementation complete, not yet submitted for review

## Overview

The `UseEtcdSteward` feature replaces etcd-backup-restore with etcd-steward as the
sidecar for etcd clusters managed by etcd-druid. It is gated behind an alpha feature
gate (`UseEtcdSteward`, disabled by default) so that all existing behavior is
unchanged when the gate is off.

Three repositories are involved. All changes are on feature branches in the
`seshachalam-yv` fork — nothing has been pushed to upstream `gardener/*`.

---

## Repository Summary

| Repository | Branch | Commits | Lines Changed | Fork Remote |
|------------|--------|---------|---------------|-------------|
| **etcd-druid** | `feat/etcd-steward-v2` | 6 | +5,884/-822 | `sesha` → `seshachalam-yv/etcd-druid` |
| **etcd-steward** | `etcd-steward-v2-local` → fork `feat/issue-1/etcd-steward-v2` | 21 | +19,500/-881 | `fork` → `seshachalam-yv/etcd-steward` |
| **etcd-wrapper** | `feat/steward-compat` + 1 uncommitted file | 2 + dirty | +501/-30 + 42 lines | `sesha` → `seshachalam-yv/etcd-wrapper` |

---

## 1. etcd-druid (`feat/etcd-steward-v2`)

### Branch Location
- **Local**: worktree at `.worktrees/etcd-steward-v2/` on branch `feat/etcd-steward-v2`
- **Fork**: `seshachalam-yv/etcd-druid` branch `feat/etcd-steward-v2` (pushed 2026-04-25)
- **Base**: `origin/master` (`b211bffb4`)

### Commits (6)
```
93752d6 Add UseEtcdSteward design specs, architecture docs, and rollout plan
ce2b592 Run make generate for EtcdMember CRD + fix features test race
1fd12b5 Wire EtcdMember into reconciler, stop snapshot leases, migrate compaction
235408a Add EtcdMember CRD to Helm chart, RBAC, and feature gate values
68f2f8b Add EtcdMember CRD types and component (DEP-04)
5b3b75a Add UseEtcdSteward feature gate (alpha)
```

### What Changed (31 source files + 15 doc files)

**Feature Gate & Constants:**
- `api/config/v1alpha1/features.go` — `UseEtcdSteward` alpha gate registration
- `api/config/v1alpha1/features_test.go` — per-subtest feature gate to avoid race
- `internal/common/constants.go` — `ImageKeyEtcdSteward`, `ComponentNameEtcdMember`
- `internal/images/images.yaml` — `etcd-steward` image entry
- `internal/utils/image.go` — return steward image key when gate enabled
- `charts/values.yaml` — `UseEtcdSteward: false` in both featureGates blocks
- `test/utils/constants.go`, `test/utils/imagevector.go` — test image vector

**EtcdMember CRD (DEP-04):**
- `api/core/v1alpha1/etcdmember_types.go` — EtcdMember spec/status types, state machine constants
- `api/core/v1alpha1/zz_generated.deepcopy.go` — generated deepcopy
- `api/core/v1alpha1/register.go` — scheme registration
- CRD YAML in `api/core/v1alpha1/crds/` and `charts/crds/`

**RBAC:**
- `charts/templates/clusterrole.yaml` — etcdmembers CRUD rules
- `internal/component/role/role.go` — `create` on leases + etcdmembers rules
- `internal/component/role/role_test.go` — updated expectations
- `internal/component/registry.go` — `EtcdMemberKind` constant

**StatefulSet Builder:**
- `internal/component/statefulset/builder.go` — `getStewardContainerCommandArgs()`, TLS volume mounts, readiness probe switch
- `internal/component/statefulset/statefulset.go` — `preSyncTaskTTLSeconds`, TLS gate check
- `internal/component/snapshotlease/snapshotlease.go` — skip when steward enabled

**Controller Wiring:**
- `internal/controller/etcd/reconciler.go` — register etcdmember component
- `internal/controller/etcd/reconcile_spec.go` — sync order insertion, cleanup list
- `internal/controller/etcd/reconcile_status.go` — `mutateETCDStatusFromEtcdMembers()` with lease fallback
- `internal/controller/compaction/reconciler.go` — read revisions from EtcdMember status
- `internal/controller/compaction/register.go` — EtcdMember predicate, `Owns(&EtcdMember{})`
- `internal/controller/compaction/snapshot.go` — POST instead of GET, skip TLS
- `internal/controller/etcdopstask/handler/utils/etcdbrclient.go` — skip backup TLS
- `internal/controller/etcdopstask/register.go` — force-delete predicate
- `internal/health/status/check.go` — export `ExecuteConditionChecks()`

**EtcdMember Component:**
- `internal/component/etcdmember/etcdmember.go` — creates/updates EtcdMember CRs per replica
- `internal/component/etcdmember/register.go` — component registration

**E2E & Docs:**
- `test/e2e/controller/etcd_test.go` — `USE_ETCD_STEWARD` env var
- `test/e2e/testenv/testenv.go` — steward-aware snapshot jobs, pod filtering
- `docs/design/*` — 8 OpenSpec design documents
- `docs/concepts/*` — architecture and EtcdMember implementation guides
- `docs/deployment/use-etcd-steward.md` — migration guide
- `docs/proposals/07-*`, `docs/proposals/08-*` — proposals
- `docs/plans/*`, `docs/USE-ETCD-STEWARD.md`, `mkdocs.yml`, `docs/README.md`

### Planned PR Chain (6 PRs)
See [`docs/design/05-pr-plan.md`](05-pr-plan.md) for the full breakdown:
1. Feature Gate + Constants + Image Vector (8 files, ~35 lines)
2. EtcdMember CRD Types + DeepCopy (3 files, ~676 lines)
3. RBAC Additions (4 files, ~55 lines)
4. StatefulSet Builder + Image Selection (4 files, ~135 lines)
5. Controller Wiring + Status Reconciliation (10 files, ~275 lines)
6. E2E Test Infrastructure + Documentation (8 files, ~175 lines)

---

## 2. etcd-steward (`etcd-steward-v2-local`)

### Branch Location
- **Local**: `etcd-steward-v2-local` in `/Users/I568019/go/src/github.com/gardener/etcd-steward`
- **Fork**: `seshachalam-yv/etcd-steward` branch `feat/issue-1/etcd-steward-v2` (needs push — see push script below)
- **Base**: `origin/master` (repo scaffold, no real sidecar code)

### Commits (21 on top of master)
```
03eefe0 Fix: transition New→Initializing→Started for empty data dir path
2d6deea Fix: skip empty subState in K8s patch to avoid CRD validation error
02b2479 Fix K8sStateRecorder: always patch EtcdMember status on every sync
e583c93 Wire DEP-04 transitions into daemon + EtcdMember K8s status patches
055305d Wire lease InfoFunc for memberID:role HolderIdentity format
36e678f Rewrite statemachine to DEP-04 + fix lease HolderIdentity format
faea543 Fix init status reset + generate config from flags (not raw ConfigMap)
2e32409 Restore backward-compatible /initialization/* and /config endpoints
875eac6 Switch to Notes Option-1: steward orchestrates, wrapper executes
58cf0bc Rewrite daemon with full production wiring (Notes Option-1)
a3313b0 Fix wrapper compatibility: plain text init status + valid etcd config
1c9471a Add TLS e2e tests and load test infrastructure
c3fcdb4 Add integration tests and benchmarks
21bec97 Add developer, concepts, and usage documentation
dd9ca4e Add data validation, GC retention policies, error handling hardening
473a0f6 Add full compaction cycle and cross-provider copy-backups
d5c689a Add snapshot info provider and supplementary member data
40081f3 Add multi-node coordination: learner join, promote, member remove
db3d64d Add S3, GCS, ABS cloud provider snapstore implementations
ad0d1a6 Implement real delta snapshots and delta restoration
6dcf638 Implement etcd-steward v2 from scratch
```

### Architecture (20 internal packages)
```
cmd/etcdsteward/
├── main.go              # CLI entry point, flag parsing, daemon wiring
├── compact/             # Standalone compaction subcommand
└── copybackups/         # Cross-provider backup copy subcommand

internal/
├── alarm/               # etcd alarm monitoring
├── bootstrapper/        # Orchestrates init: talks to wrapper, starts restore
├── compactor/           # Periodic compaction + defragmentation
├── compression/         # gzip snapshot compression
├── config/              # Flag parsing, config generation from flags
├── defrag/              # Scheduled defragmentation
├── errors/              # Structured error types
├── etcdclient/          # etcd client wrapper with lock support
├── gc/                  # Snapshot garbage collection with retention policies
├── leaderwatch/         # etcd leader election monitoring
├── lease/               # Kubernetes lease management (memberID:role identity)
├── member/              # EtcdMember CRD updater + K8s state recorder
├── metrics/             # Prometheus metrics (init duration, snapshots, state)
├── restorer/            # Full + delta snapshot restoration
├── server/              # HTTP server (/initialization/*, /config, /healthz, /snapshot/*, /defrag)
├── snapshotter/         # Full + delta snapshot pipeline
├── snapstore/           # S3, GCS, ABS, local snapshot storage
├── statemachine/        # DEP-04 state machine (New→Initializing→Starting→Started)
└── validator/           # Snapshot data validation
```

### Test Coverage
- 34 `*_test.go` files across all packages
- Integration tests: `test/integration/` (restorer, snapshotter)
- E2E tests: `test/e2e/` (full lifecycle, TLS, snapshotter)
- Load tests: `test/load/` (concurrent write/read benchmark)
- Benchmarks: compression, snapshotter

### HTTP API (backward-compatible with etcd-backup-restore)
```
GET  /healthz                 # Health check
GET  /config                  # Current etcd config
POST /initialization/start    # Start init (steward orchestrates)
GET  /initialization/status   # Init progress
POST /snapshot/full           # Trigger full snapshot
GET  /snapshot/latest         # Latest snapshot info
POST /defrag                  # Trigger defragmentation
GET  /metrics                 # Prometheus metrics
```

---

## 3. etcd-wrapper (`feat/steward-compat`)

### Branch Location
- **Local**: `feat/steward-compat` in `/Users/I568019/go/src/github.com/gardener/etcd-wrapper`
- **Fork**: `seshachalam-yv/etcd-wrapper` (needs push — see push script)
- **Base**: `origin/main` (`9ee5717`)

### Commits (2 on top of main)
```
600c85a Dual-mode initialization: support both backup-restore and etcd-steward flows
86344fc Add etcd-steward compatibility endpoints
```

### Uncommitted Change (1 file)
`internal/bootstrap/bootstrap.go` — `applyPeerSkipClientSANVerify()`:
Fixes `embed.ConfigFromFile()` not mapping `skip-client-san-verification` to
`PeerTLSInfo.SkipClientSANVerify`. Parses raw YAML to apply it manually.
Handles both etcd 3.4 (`experimental-peer-skip-client-san-verification`) and
etcd 3.5+ (`peer-transport-security.skip-client-san-verification`).

### What Changed (8 files, +501/-30 lines)
- `internal/app/app.go` — dual-mode init: detect steward vs backup-restore, switch HTTP/HTTPS
- `internal/app/leadership.go` — leadership detection for steward flow
- `internal/app/readycheck.go` — steward-aware readiness check (HTTP `/healthz`)
- `internal/app/steward_test.go` — 245 lines of steward compat tests
- Build workflow files updated for CI

---

## How to Build & Test

### etcd-steward (local build)
```bash
cd /Users/I568019/go/src/github.com/gardener/etcd-steward
git checkout etcd-steward-v2-local
make build                    # Builds etcdsteward binary
make test                     # Unit tests (go test ./...)
# E2E with KinD:
hack/kind-e2e.sh
```

### etcd-wrapper (local build)
```bash
cd /Users/I568019/go/src/github.com/gardener/etcd-wrapper
git checkout feat/steward-compat
make build
make verify
```

### etcd-druid (with steward gate)
```bash
cd /Users/I568019/go/src/github.com/gardener/etcd-druid
git checkout feat/etcd-steward-v2    # or use the worktree
make test-unit
make test-integration
# E2E with steward gate:
USE_ETCD_STEWARD=true make test-e2e
```

### Full E2E (KinD, all 3 repos)
See [`docs/design/06-e2e-testing-spec.md`](06-e2e-testing-spec.md) for the complete
E2E test setup, including local registry, image overrides, and the `deploy-steward-dev`
make target.

---

## Push Script

Run this when GitHub is reachable to push all remaining branches:

```bash
#!/bin/bash
set -euo pipefail

echo "=== Pushing etcd-steward ==="
cd /Users/I568019/go/src/github.com/gardener/etcd-steward
git push fork etcd-steward-v2-local:feat/issue-1/etcd-steward-v2 --force-with-lease

echo "=== Pushing etcd-wrapper ==="
cd /Users/I568019/go/src/github.com/gardener/etcd-wrapper
# First commit the dirty working tree change
git stash
git checkout feat/steward-compat
git stash pop
git add internal/bootstrap/bootstrap.go
git commit -m "Fix peer TLS skip-client-san-verification not applied from config file

embed.ConfigFromFile() does not map skip-client-san-verification to
PeerTLSInfo.SkipClientSANVerify. Parse raw YAML to detect both the etcd 3.4
experimental flag and etcd 3.5+ peer-transport-security sub-field."
git push sesha feat/steward-compat -u
git checkout main

echo "=== Done ==="
echo "Branches pushed:"
echo "  etcd-druid:   seshachalam-yv/etcd-druid   feat/etcd-steward-v2"
echo "  etcd-steward: seshachalam-yv/etcd-steward  feat/issue-1/etcd-steward-v2"
echo "  etcd-wrapper: seshachalam-yv/etcd-wrapper  feat/steward-compat"
```

---

## What's Left Before PRs

### Cleanup Required
1. **Image registry**: `internal/images/images.yaml` has `localhost:5001/etcd-steward` — must change to `europe-docker.pkg.dev/gardener-project/releases/gardener/etcd-steward` with a real tag
2. **etcd-steward release**: Need `v0.1.0` tag on etcd-steward before druid PRs can reference it
3. **etcd-wrapper release**: Need `v0.7.1` or `v0.8.0` tag with steward-compat changes
4. **Binary in git**: `etcd-steward/etcdsteward` binary committed — must be gitignored
5. **Split druid branch into 6 PRs**: Per the PR plan in `docs/design/05-pr-plan.md`

### Cross-Repo Release Order
1. `gardener/etcd-steward` → `v0.1.0`
2. `gardener/etcd-wrapper` → `v0.7.1` (or next minor)
3. `gardener/etcd-druid` → PRs 1-6 (incremental)

### Test Matrix Before Submission
| Test | Command | Status |
|------|---------|--------|
| Druid unit tests | `make test-unit` | Must verify |
| Druid integration tests | `make test-integration` | Must verify |
| Druid e2e (gate=false) | `make test-e2e` | Must verify no regression |
| Druid e2e (gate=true) | `USE_ETCD_STEWARD=true make test-e2e` | Previously 29/29 pass |
| Steward unit tests | `make test` | Must verify |
| Wrapper unit tests | `make verify` | Must verify |

---

## Design Documents

All in `docs/design/` on the `feat/etcd-steward-v2` branch:

| Doc | File | Purpose |
|-----|------|---------|
| Why etcd-steward | `01-why-etcd-steward.md` | Problem statement, architecture decisions |
| Druid changes spec | `02-druid-changes-spec.md` | Every druid change with RFC 2119 language |
| Steward internals | `03-steward-spec.md` | 17 packages, 11-phase startup, state machine |
| Wrapper compat | `04-wrapper-spec.md` | Dual-mode init, new endpoints |
| PR plan | `05-pr-plan.md` | 6 PRs, file lists, test matrix |
| E2E testing | `06-e2e-testing-spec.md` | Results, issues, reproduction steps |
| Proposal | `proposal.md` | High-level proposal (capabilities, impact) |

## Other Branches (Historical, Superseded)

These branches exist locally but are superseded by the v2 branches above:

| Repo | Branch | Status |
|------|--------|--------|
| etcd-steward | `etcd-steward-claude` | Superseded — phase 1-15 incremental builds |
| etcd-steward | `ai/etcd-steward/claude/production-ready-v2` | Superseded — production-ready attempt |
| etcd-druid | `feat/use-etcd-steward-feature-gate` | Subset — only the feature gate commit |
| etcd-druid | `feature/etcd-steward-integration` | Superseded — older integration attempt |
