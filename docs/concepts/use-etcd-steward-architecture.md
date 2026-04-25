---
title: "UseEtcdSteward Architecture"
---

# UseEtcdSteward Architecture

This document illustrates the architectural differences between the two sidecar paths controlled by the `UseEtcdSteward` feature gate, the data flow for member status reporting, and the toggle mechanism.

## Sidecar Selection: Two Paths

The `UseEtcdSteward` feature gate controls which sidecar container is deployed alongside etcd-wrapper in each etcd member pod.

```
                        +---------------------+
                        | UseEtcdSteward gate |
                        +----------+----------+
                                   |
                    +--------------+--------------+
                    |                             |
               gate = false                  gate = true
               (default)                     (opt-in)
                    |                             |
                    v                             v
    +-------------------------------+  +-------------------------------+
    |  etcd-backup-restore sidecar  |  |    etcd-steward sidecar       |
    +-------------------------------+  +-------------------------------+
    | Image: etcd-backup-restore    |  | Image: etcd-steward           |
    | Protocol: HTTPS (TLS)         |  | Protocol: HTTP (plain)        |
    | Status: via member leases     |  | Status: via EtcdMember CRs    |
    | Snapshots: via snapshot leases|  | Snapshots: via EtcdMember CRs |
    | Probe: HTTPS /healthz         |  | Probe: HTTP /healthz          |
    +-------------------------------+  +-------------------------------+
```

## Pod Architecture

### Gate Disabled (backup-restore)

```
    +------------------------------------------+
    |              etcd member pod              |
    |                                          |
    |  +-----------------+ +-----------------+ |
    |  |  etcd-wrapper   | | backup-restore  | |
    |  |                 | |                 | |
    |  | etcd process    | | HTTPS server    | |
    |  | :2379 (client)  | |   :8080         | |
    |  | :2380 (peer)    | |                 | |
    |  | :9095 (wrapper) | | TLS: enabled    | |
    |  |                 | |                 | |
    |  | readiness probe:| | Updates:        | |
    |  | HTTPS GET       | |  member lease   | |
    |  | backup-restore  | |  snapshot lease  | |
    |  | /healthz        | |  (full + delta) | |
    |  +-----------------+ +-----------------+ |
    +------------------------------------------+
                       |
                       v
               +---------------+
               | Member Lease  |      +-------------------+
               | (HolderID:    |      | Snapshot Leases   |
               |  memberID:    |      | (full + delta)    |
               |  role)        |      | (HolderID:        |
               +-------+-------+      |  revision)        |
                       |              +--------+----------+
                       |                       |
                       v                       v
               +--------------------------------------+
               |          etcd-druid                   |
               | Status reconciler reads leases        |
               | --> Etcd.Status.Members               |
               +--------------------------------------+
```

### Gate Enabled (etcd-steward)

```
    +------------------------------------------+
    |              etcd member pod              |
    |                                          |
    |  +-----------------+ +-----------------+ |
    |  |  etcd-wrapper   | | etcd-steward    | |
    |  |                 | |                 | |
    |  | etcd process    | | HTTP server     | |
    |  | :2379 (client)  | |   :8080         | |
    |  | :2380 (peer)    | |                 | |
    |  | :9095 (wrapper) | | TLS: disabled   | |
    |  |                 | |                 | |
    |  | readiness probe:| | Updates:        | |
    |  | HTTP GET        | |  EtcdMember CR  | |
    |  | steward         | |  (status sub-   | |
    |  | /healthz        | |   resource)     | |
    |  +-----------------+ +-----------------+ |
    +------------------------------------------+
                       |
                       v
             +-------------------+
             | EtcdMember CR     |
             | status:           |
             |   state: Started  |
             |   subState: Leader|
             |   snapshots:      |
             |     lastFull: ... |
             |     lastDelta: ...|
             |   id: <memberID>  |
             |   clusterID: ...  |
             +--------+----------+
                      |
                      v
             +--------------------------------------+
             |          etcd-druid                   |
             | Status reconciler reads EtcdMember    |
             | --> Etcd.Status.Members               |
             +--------------------------------------+
```

## Data Flow: Status Reporting

The following diagram shows how member status information flows from the sidecar to the `Etcd.Status` resource, under both configurations.

```
Gate = false (backup-restore):

  backup-restore sidecar
       |
       | renews member lease (HolderIdentity: "<memberID>:<clusterID>:<role>")
       | renews snapshot leases (HolderIdentity: "<revision>")
       |
       v
  Kubernetes Lease resources
       |
       | druid status reconciler reads leases
       | maps lease HolderIdentity to member status
       |
       v
  Etcd.Status.Members[]
       |
       v
  Etcd conditions (AllMembersReady, BackupReady, etc.)


Gate = true (etcd-steward):

  etcd-steward sidecar
       |
       | updates EtcdMember.Status (state, subState, snapshots, id, clusterID)
       | (also renews member lease for backward compatibility)
       |
       v
  EtcdMember custom resource
       |
       | druid status reconciler reads EtcdMember.Status
       | maps state/subState to Ready/NotReady/Unknown
       |
       v
  Etcd.Status.Members[]
       |
       v
  Etcd conditions (AllMembersReady, BackupReady, etc.)
```

## Feature Gate Toggle Mechanism

Switching the feature gate changes the StatefulSet spec, which triggers a rolling update of etcd member pods.

```
  Operator sets UseEtcdSteward = true
       |
       v
  etcd-druid reconciles Etcd resource
       |
       +-- Creates EtcdMember CRs (one per replica)
       +-- Skips snapshot lease creation
       +-- Updates StatefulSet:
       |     container image: etcd-steward
       |     container args: steward CLI args
       |     readiness probe: HTTP /healthz
       |     wrapper flag: --backup-restore-tls-enabled=false
       |
       v
  StatefulSet controller performs rolling update
       |
       v
  New pods start with etcd-steward sidecar
       |
       v
  etcd-steward updates EtcdMember.Status
       |
       v
  druid reads EtcdMember.Status for Etcd.Status


  ---- Rollback ----

  Operator sets UseEtcdSteward = false
       |
       v
  etcd-druid reconciles Etcd resource
       |
       +-- Stops creating EtcdMember CRs (existing CRs remain)
       +-- Re-creates snapshot leases
       +-- Updates StatefulSet:
       |     container image: etcd-backup-restore
       |     container args: backup-restore CLI args
       |     readiness probe: HTTPS /healthz
       |     wrapper flag: --backup-restore-tls-enabled=true
       |
       v
  StatefulSet controller performs rolling update
       |
       v
  New pods start with etcd-backup-restore sidecar
       |
       v
  backup-restore updates leases
       |
       v
  druid reads leases for Etcd.Status
```

!!! info "Snapshot Format Compatibility"
    The snapshot format is identical between etcd-steward and etcd-backup-restore. Both use the same snapstore library. A snapshot taken by one sidecar can be restored by the other. No snapshot migration is required when toggling the feature gate.

## Component Interaction Summary

```
+-------------------------------------------------------------------+
|                        etcd-druid                                  |
|                                                                    |
|  +-----------------------+  +----------------------------------+  |
|  | Etcd Reconciler       |  | Status Reconciler                |  |
|  |                       |  |                                  |  |
|  | Sync order:           |  | if UseEtcdSteward:               |  |
|  |  1. ServiceAccount    |  |   read EtcdMember.Status         |  |
|  |  2. Role/RoleBinding  |  |   map State -> MemberStatus      |  |
|  |  3. ConfigMap         |  |   read Snapshots for BackupReady |  |
|  |  4. Services          |  | else:                            |  |
|  |  5. PDB               |  |   read member leases             |  |
|  |  6. MemberLease       |  |   read snapshot leases           |  |
|  |  7. SnapshotLease*    |  |                                  |  |
|  |  8. EtcdMember**      |  | --> Etcd.Status.Members          |  |
|  |  9. StatefulSet       |  +----------------------------------+  |
|  |                       |                                        |
|  | * skipped if steward  |  +----------------------------------+  |
|  | ** only if steward    |  | Compaction Controller            |  |
|  +-----------------------+  |                                  |  |
|                             | if UseEtcdSteward:               |  |
|                             |   read EtcdMember.Snapshots      |  |
|                             |   POST /snapshot/full (HTTP)     |  |
|                             | else:                            |  |
|                             |   read snapshot leases           |  |
|                             |   GET /snapshot (HTTPS)          |  |
|                             +----------------------------------+  |
+-------------------------------------------------------------------+
```

## References

- [UseEtcdSteward Migration Guide](../deployment/use-etcd-steward.md)
- [DEP-04 Implementation: EtcdMember](etcdmember-implementation.md)
- [Feature Gates](../deployment/feature-gates.md)
- [Etcd Cluster Components](etcd-cluster-components.md)
- [UseEtcdSteward Rollout Plan](../proposals/08-etcd-steward-rollout-plan.md)
