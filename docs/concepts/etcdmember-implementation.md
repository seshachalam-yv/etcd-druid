---
title: "DEP-04 Implementation: EtcdMember Custom Resource"
---

# DEP-04 Implementation: EtcdMember Custom Resource

This document describes the implementation of the `EtcdMember` custom resource as specified in [DEP-04](../proposals/04-etcd-member-custom-resource.md). It covers the state machine, how etcd-steward reports status, and how etcd-druid consumes that status to populate `Etcd.Status.Members`.

!!! note
    The `EtcdMember` custom resource is only created when the [`UseEtcdSteward` feature gate](../deployment/feature-gates.md) is enabled. When the gate is disabled, member status continues to be derived from member leases.

## EtcdMember State Machine

Each etcd member progresses through a well-defined lifecycle captured as a combination of a top-level `State` and a `SubState` in `EtcdMember.Status`.

### States and Sub-States

```
 State          SubState              Description
 -----          --------              -----------
 New            (none)                Initial state for a newly created member.
 Initializing   DBValidationSanity   Sanity validation of the etcd data directory.
 Initializing   DBValidationFull     Full validation after an unclean exit.
 Initializing   Restoration          Restoring etcd DB from backup (single-node only).
 Starting       PendingLearner       Waiting to be added as a learner to the cluster.
 Starting       Learner              Added as a learner, syncing DB from leader.
 Started        Follower             Voting member, following the leader.
 Started        Leader               Voting member, handling writes and linearizable reads.
```

### Top-Level State Transitions

```
                         +--------+
                         |  New   |
                         +---+----+
                             |
               +-------------+-------------+
               |                           |
               v                           v
        +--------------+           +-----------+
        | Initializing |           |  Starting |
        |  (DBV-S/F/R) |           | (PL / L)  |
        +------+-------+           +-----+-----+
               |                         |
               +------------+------------+
                            |
                            v
                      +-----------+
                      |  Started  |
                      | (F / Ld)  |
                      +-----------+
```

**Transitions summary:**

| From | To | Trigger |
|------|----|---------|
| (nil) | New | EtcdMember CR created by druid during reconciliation |
| New | Initializing (DBValidationSanity) | Sidecar detects a previous clean exit |
| New | Initializing (DBValidationFull) | Sidecar detects a previous unclean exit |
| New | Starting (PendingLearner) | New member during scale-up (multi-node cluster) |
| Initializing (DBValidationSanity/Full) | Initializing (Restoration) | DB validation failed (single-node cluster only) |
| Initializing (DBValidationSanity/Full) | Started (Leader/Follower) | DB validation succeeded |
| Initializing (Restoration) | Started (Leader) | Restoration succeeded (single-node cluster) |
| Starting (PendingLearner) | Starting (Learner) | Successfully added as learner to the cluster |
| Starting (Learner) | Started (Follower) | DB synced with leader, promoted to voting member |
| Started (Follower) | Started (Leader) | Won leader election |
| Started (Leader) | Started (Follower) | Lost leader election |
| Started (Leader/Follower) | Initializing (DBValidationSanity/Full) | Member restarted |
| Initializing (DBValidationFull) | New | DB corruption detected, member removed from cluster |

### Single-Node Bootstrap Flow

For a cluster bootstrapped from 0 to 1 replica:

```
New --> Initializing (DBValidationSanity)
    --> Initializing (Restoration)       [if DB validation fails]
    --> Started (Leader)
```

When no prior data exists, the single member restores from the latest backup (if available) and starts directly as the leader.

### Multi-Node Scale-Up Flow

When scaling from 1 to N replicas, new members follow:

```
New --> Starting (PendingLearner)
    --> Starting (Learner)
    --> Started (Follower)
```

The existing leader remains in `Started (Leader)` throughout the scale-up.

### Member Restart Flow

When a voting member in a multi-node cluster restarts:

```
Started (Leader/Follower) --> Initializing (DBValidationSanity or DBValidationFull)
                          --> Started (Leader/Follower)       [if DB is valid]
                          --> New                             [if DB is corrupt]
```

If the DB is corrupt, the member is removed from cluster membership and transitions back to `New`, after which it follows the scale-up flow.

## How etcd-steward Reports to EtcdMember.Status

The etcd-steward sidecar container runs in each etcd member pod and is responsible for updating the `EtcdMember` custom resource's `Status` subresource. The sidecar's ServiceAccount has RBAC permissions to `get`, `update`, and `patch` the `etcdmembers/status` subresource.

### Fields Updated by Steward

| Field | Description | Update Frequency |
|-------|-------------|-----------------|
| `status.state` | Top-level lifecycle state (New, Initializing, Starting, Started) | On every state transition |
| `status.subState` | Sub-state within the top-level state | On every state transition |
| `status.id` | Etcd member ID (assigned by etcd) | Once, after member joins the cluster |
| `status.clusterID` | Etcd cluster ID | Once, after member joins the cluster |
| `status.peerTLSEnabled` | Whether peer TLS is active for this member | On startup and TLS config changes |
| `status.snapshots.lastFull` | Metadata of the last full snapshot | After each successful full snapshot |
| `status.snapshots.lastDelta` | Metadata of the last delta snapshot | After each successful delta snapshot |
| `status.snapshots.accumulatedDeltaSize` | Total size of delta snapshots since last full snapshot | After each delta snapshot |
| `status.transitions` | Ordered list of state transitions with timestamps and reasons | On every state transition |
| `status.lastRestoration` | Status of the most recent DB restoration | During and after restoration |
| `status.lastDefragmentation` | Status of the most recent defragmentation | During and after defragmentation |
| `status.dbSize` | Total etcd DB size | Periodically |
| `status.dbSizeInUse` | Logical etcd DB size (excluding free pages) | Periodically |

### Member Lease Compatibility

During the Alpha stage, etcd-steward also renews the member lease with `HolderIdentity` in the format `<memberID>:<clusterID>:<role>`. This maintains backward compatibility with the legacy status check path. The member lease renewal can be controlled via the `--enable-snapshot-lease-renewal` flag.

## How Druid Reads EtcdMember.Status

The etcd-druid operator reads `EtcdMember` custom resources during status reconciliation to populate `Etcd.Status.Members`.

### Status Reconciliation Flow

```
1. Etcd status sync timer fires (default: every 15 seconds)
2. Druid lists all EtcdMember CRs owned by the Etcd resource
   (label selector: gardener.cloud/owned-by=<etcd-name>)
3. For each EtcdMember:
   a. Read status.state and status.subState
   b. Map to legacy EtcdMemberStatus:
      - Started (Leader/Follower) --> Ready
      - Initializing / Starting   --> NotReady
      - New / unknown             --> Unknown
   c. Read status.id for the member ID
   d. Read status.snapshots for backup health assessment
4. Populate Etcd.Status.Members with the mapped statuses
5. Update Etcd conditions (AllMembersReady, BackupReady, etc.)
```

### State-to-MemberStatus Mapping

| EtcdMember State | EtcdMember SubState | Etcd.Status.Members Status | Role |
|-----------------|--------------------|-----------------------------|------|
| Started | Leader | Ready | Leader |
| Started | Follower | Ready | Member |
| Initializing | any | NotReady | Unknown |
| Starting | PendingLearner | NotReady | Learner |
| Starting | Learner | NotReady | Learner |
| New | (none) | Unknown | Unknown |

### Fallback to Lease-Based Status

During the transition period, if an `EtcdMember` resource exists but its `State` field has not been populated (the steward container has not yet started reporting), the status reconciler falls back to reading the member lease's `HolderIdentity` to determine the member's role. This ensures a smooth rolling update where some pods may still be running the old sidecar.

## EtcdMember Lifecycle Management

### Creation

Druid creates `EtcdMember` resources during `Etcd` resource reconciliation, before creating or updating the StatefulSet. One `EtcdMember` is created per replica defined in `Etcd.Spec.Replicas`.

Each `EtcdMember` is created with:

- **Name:** `<etcd-name>-<ordinal>` (matching the StatefulSet pod name)
- **Namespace:** Same as the parent `Etcd` resource
- **Owner reference:** Points to the parent `Etcd` resource
- **Labels:** `gardener.cloud/owned-by=<etcd-name>`

During scale-up, newly created `EtcdMember` resources receive the annotation `druid.gardener.cloud/create-as-learner` to signal to the steward that the member should join as a learner rather than starting a new cluster.

### Deletion

`EtcdMember` resources are deleted by druid in the following scenarios:

1. **Etcd resource deletion:** All `EtcdMember` resources are deleted as part of the cleanup flow.
2. **Scale-down:** Surplus `EtcdMember` resources (ordinals beyond the new replica count) are deleted.
3. **Hibernation (replicas=0):** All `EtcdMember` resources are deleted because stale member state could cause incorrect controller actions on un-hibernation.

### Sync Order

When `UseEtcdSteward` is enabled, the `EtcdMember` component is synced **before** the `StatefulSet` component in the reconciliation order. This ensures that `EtcdMember` CRs exist before pods start, so the steward sidecar can immediately find and update its corresponding `EtcdMember` resource.

## Compaction Controller Integration

The compaction controller reads snapshot revision information from `EtcdMember.Status.Snapshots` (instead of snapshot leases) to determine when to trigger a compaction Job:

1. Find the leading member's `EtcdMember` resource (state=Started, subState=Leader).
2. Read `snapshots.lastFull.endRevision` and `snapshots.lastDelta.endRevision`.
3. Compute accumulated delta revisions.
4. If the delta exceeds the configured threshold, create a compaction Job.

When triggering a full snapshot for compaction, the compaction controller sends an HTTP POST to the steward's `/snapshot/full` endpoint (instead of an HTTPS GET to backup-restore).

## RBAC Requirements

The etcd-steward sidecar requires RBAC permissions to operate on `EtcdMember` resources. These are granted via the per-etcd `Role`:

```yaml
# EtcdMember resources
- apiGroups: ["druid.gardener.cloud"]
  resources: ["etcdmembers"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: ["druid.gardener.cloud"]
  resources: ["etcdmembers/status"]
  verbs: ["get", "update", "patch"]
```

The etcd-druid `ClusterRole` additionally includes `create`, `delete`, and `deletecollection` verbs for managing `EtcdMember` lifecycle.

## References

- [DEP-04: EtcdMember Custom Resource (Proposal)](../proposals/04-etcd-member-custom-resource.md)
- [UseEtcdSteward Migration Guide](../deployment/use-etcd-steward.md)
- [Feature Gates](../deployment/feature-gates.md)
- [Etcd Cluster Components](etcd-cluster-components.md)
