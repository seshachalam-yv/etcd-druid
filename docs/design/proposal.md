# Proposal: Replace etcd-backup-restore with etcd-steward

## Why

etcd-backup-restore is a monolithic sidecar that has grown to handle snapshots, restore,
compaction, defrag, GC, leader election, health checks, and HTTP initialization protocol —
all in a single binary with a single Ginkgo test suite. The initialization flow requires
the wrapper to poll the sidecar for status, creating a tight coupling. Member state is
encoded in lease HolderIdentity strings with no structured schema.

etcd-steward replaces this with a modular sidecar where each concern is a separate package
with Go native tests. The initialization flow is inverted (steward orchestrates, wrapper
executes). Member state moves to the EtcdMember Custom Resource with a DEP-04 compliant
state machine.

**Why now**: The EtcdMember CRD (DEP-04) provides the structured status API that the
current lease-based approach cannot support. This is a prerequisite for future features
like coordinated defragmentation, learner promotion, and live cluster migration.

## What Changes

- **ADDED**: `UseEtcdSteward` alpha feature gate (disabled by default)
- **ADDED**: `EtcdMember` CRD for per-member lifecycle tracking
- **ADDED**: etcd-steward sidecar image in image vector
- **MODIFIED**: StatefulSet builder generates steward args when gate enabled
- **MODIFIED**: Status reconciler reads from EtcdMember CRDs (with lease fallback)
- **MODIFIED**: Compaction controller reads snapshot revisions from EtcdMember.Status
- **MODIFIED**: Snapshot/compaction HTTP uses POST + HTTP (not GET + HTTPS) for steward
- **MODIFIED**: Peer TLS check bypassed when steward manages TLS config
- **MODIFIED**: RBAC adds `create` on leases + etcdmembers permissions
- **NOT CHANGED**: Any behavior when `UseEtcdSteward=false` (the default)

## Capabilities

### New Capabilities
- `etcdmember-lifecycle`: Per-member K8s resource with state machine (New → Initializing → Starting → Started)
- `steward-sidecar`: Lean modular sidecar with 17 packages, Go native tests
- `feature-gate-toggle`: Live switch between steward and backup-restore without data loss

### Modified Capabilities
- `status-reconciliation`: Dual-source (EtcdMember CRDs when gate=true, leases when gate=false)
- `compaction-trigger`: Dual-source (EtcdMember.Status.Snapshots vs snapshot leases)
- `snapshot-api`: POST /snapshot/full (steward) vs GET /snapshot/full (backup-restore)

## Impact

### Repositories
| Repo | Files Changed | Lines | Risk |
|------|--------------|-------|------|
| gardener/etcd-druid | 28 | +979/-55 | Medium (all gated) |
| gardener/etcd-steward | 14 | new sidecar | New repo |
| gardener/etcd-wrapper | 12 | +501/-30 | Low (backward compat) |

### Data Safety
- Snapshot format: identical (same snapstore library)
- Toggle test: verified steward → backup-restore → zero data loss
- Rollback: disable gate → pods restart with backup-restore in ~40s

### Breaking Changes
**None.** Feature gate defaults to false. All changes are additive or gated.
