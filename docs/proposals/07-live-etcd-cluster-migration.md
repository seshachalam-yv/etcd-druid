---
title: Live Etcd Cluster Migration
dep-number: 07
creation-date: 2025-12-23
status: implementable
authors:
- "@I568019"
reviewers:
- TBD
---

# DEP-07: Live Etcd Cluster Migration

## Summary

This proposal introduces a zero-downtime etcd cluster migration capability that enables live Control Plane Migration (CPM) across different regions, zones, and cloud providers. The design follows CNCF best practices for Kubernetes operators and etcd cluster management, introducing a dedicated `EtcdMigration` custom resource, comprehensive observability, automatic disaster recovery, and a phased implementation approach.

## Motivation

### Current Challenge

Today, migrating an etcd cluster to new infrastructure requires:

- Taking a snapshot of the source cluster
- Stopping all API servers
- Restoring the snapshot on the target cluster
- Restarting API servers

This approach results in **significant downtime** and is operationally risky, especially for production control planes managing critical workloads.

### Use Cases

1. **Cross-Region Migration**: Moving etcd clusters between geographic regions for latency or compliance requirements
2. **Cloud Provider Migration**: Transitioning from one cloud provider to another (e.g., GCP to AWS)
3. **Infrastructure Upgrades**: Moving to new infrastructure without downtime
4. **Disaster Recovery**: Planned failover to standby infrastructure

### Goals

- Enable **zero-downtime** etcd cluster migration across different infrastructures
- Follow Kubernetes operator best practices with declarative CRDs and reconciliation loops
- Provide comprehensive observability through Prometheus metrics and structured logging
- Implement automatic disaster recovery with snapshots and rollback capabilities
- Support gradual rollout with manual promotion gates
- Ensure data consistency and cluster health throughout migration
- Maintain cloud-agnostic and portable design

### Non-Goals

- Supporting etcd version migrations (handled separately through upgrade procedures)
- Migrating between different etcd storage backends
- Application-level data migration (focus is on etcd cluster infrastructure)
- Real-time data replication (uses etcd's native learner mechanism)

## Proposal

### Architecture Overview

The migration process follows a well-defined state machine with 10 distinct phases:

```
Preparing → PreparingSource → ProvisioningTarget → Joining → 
Syncing → ReadyForPromotion → Promoting → RemovingSource → 
Finalizing → Completed
```

### Key Components

#### 1. EtcdMigration Custom Resource (NEW)

A dedicated CRD that models migration as a first-class operation:

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdMigration
metadata:
  name: migrate-to-region-b
spec:
  sourceEtcdRef:
    name: etcd-source
  
  targetEtcdSpec:
    replicas: 3
    # Full Etcd specification
  
  strategy:
    type: LiveMigration
    liveMigration:
      autoProgression: false
      promotionReadiness:
        minSyncDuration: 5m
        maxLagBytes: 1048576
  
  networking:
    sourceExternalEndpoints:
      peerURLs:
        - name: etcd-source-0
          urls: ["https://10.0.1.10:2380"]
    targetExternalEndpoints:
      peerURLs:
        - name: etcd-target-0
          urls: ["https://10.0.2.10:2380"]

status:
  phase: ReadyForPromotion
  conditions: [...]
  migrationMetrics:
    syncLagBytes: 0
    syncDuration: "25m"
  memberStatus: [...]
```

#### 2. Enhanced Etcd CR Fields

Extend the existing Etcd CR to support migration scenarios:

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: Etcd
spec:
  etcd:
    # External peer URLs for cross-cluster communication
    additionalAdvertisePeerUrls:
      - name: etcd-target-0
        urls: ["https://10.0.2.10:2380"]
    
    # Bootstrap with existing cluster
    bootstrapWithExistingCluster:
      joinMode: learner
      members:
        - name: etcd-source-0
          peerUrls:
            - "http://etcd-source-0.etcd-source-peer.svc:2380"
            - "https://10.0.1.10:2380"
      clientEndpoints:
        - "https://source-lb.example.com:2379"
```

#### 3. Migration Controller

A new controller that orchestrates the end-to-end migration lifecycle:

**Responsibilities:**

- Validate source cluster health
- Create automatic pre-migration snapshots
- Provision and configure target cluster
- Monitor data synchronization
- Enforce promotion gates
- Execute rollback on failures
- Clean up after completion

**Reconciliation Pattern:**

```go
func (r *MigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    migration := &druidv1alpha1.EtcdMigration{}
    // ... get migration resource
    
    switch migration.Status.Phase {
    case Preparing:
        return r.reconcilePrepareSource(ctx, migration)
    case ProvisioningTarget:
        return r.reconcileProvisionTarget(ctx, migration)
    case Joining:
        return r.reconcileJoinMembers(ctx, migration)
    // ... handle each phase
    }
}
```

### Migration Workflow

**Phase 1: Prepare Source**

- Validate source etcd cluster health
- Take automatic snapshot for disaster recovery
- Configure external peer URLs via LoadBalancer/NodePort
- Update status with snapshot information

**Phase 2: Provision Target**

- Create target Etcd CR with `bootstrapWithExistingCluster`
- Configure external peer URLs for target
- Wait for target pods to be ready

**Phase 3: Join Members**

- Target members join source cluster as **learners**
- Learners sync data from voting members
- No impact on cluster quorum

**Phase 4: Sync Data**

- Monitor synchronization progress
- Track lag in bytes and duration
- Update metrics continuously

**Phase 5: Promotion Gate**

- Validate promotion readiness criteria:
  - Minimum sync duration met
  - Sync lag below threshold
  - All target members healthy
- Wait for manual approval (if `autoProgression: false`)

**Phase 6: Promote Target**

- Promote learners to voting members
- Target cluster now participates in quorum

**Phase 7: Remove Source**

- Create `EtcdOpsTask` to remove source members
- Gracefully remove old members one by one
- Maintain quorum throughout removal

**Phase 8: Finalize**

- Remove `additionalAdvertisePeerUrls` from target
- Remove `bootstrapWithExistingCluster` from target
- Update status to Completed

### Observability

**Prometheus Metrics:**

```
etcd_migration_phase{migration_name, namespace}
etcd_migration_sync_lag_bytes{migration_name, namespace}
etcd_migration_duration_seconds{migration_name, namespace, phase}
etcd_migration_member_status{migration_name, namespace, member_name, cluster}
```

**PrometheusRule Alerts:**

- `EtcdMigrationStuck`: Migration stuck in phase for >30 minutes
- `EtcdMigrationSyncLagHigh`: Sync lag exceeds 10MB for >10 minutes
- `EtcdMigrationFailed`: Migration entered Failed state

**Structured Logging:**

```go
log.Info("Target member joined as learner",
    "migration", migration.Name,
    "member", memberName,
    "syncProgress", syncProgress)
```

### Disaster Recovery

**Automatic Snapshot:**

- Pre-migration snapshot created automatically
- Stored with migration metadata
- Referenced in migration status

**Automatic Rollback:**

```go
// Triggered on:
// - Sync timeout (configurable)
// - Health check failures (threshold)
// - Manual intervention

func (r *MigrationReconciler) reconcileRollback(ctx, migration) {
    // 1. Remove target learners
    // 2. Delete target cluster
    // 3. Restore source configuration
    // 4. Update status to RolledBack
}
```

### Security

**TLS Requirements:**

- External URLs must use HTTPS in production
- Certificates must include SANs for all advertised URLs
- Integration with cert-manager for automated certificate management

**RBAC:**

- Dedicated ClusterRole for MigrationController
- Principle of least privilege
- Separate roles for viewing vs. managing migrations

**Audit Logging:**

- All migration operations logged
- Integration with Kubernetes audit policy

### API Validation

**CEL Validation Rules:**

```yaml
# Source reference is immutable
- rule: "!has(oldSelf.spec.sourceEtcdRef) || 
         self.spec.sourceEtcdRef == oldSelf.spec.sourceEtcdRef"

# Peer URL count must match replicas
- rule: "size(self.spec.networking.sourceExternalEndpoints.peerURLs) == 
         self.spec.targetEtcdSpec.replicas"

# Auto-progression requires criteria
- rule: "!self.spec.strategy.liveMigration.autoProgression ||
         has(self.spec.strategy.liveMigration.promotionReadiness)"
```

### Implementation Phases

**Phase 1: API and Core Controllers** (Weeks 1-3)

- Define `EtcdMigration` CRD with CEL validations
- Extend `Etcd` CRD with new fields
- Implement `MigrationController` scaffolding

**Phase 2: Source Preparation** (Weeks 4-5)

- Source health checks
- Automatic snapshot creation
- External peer URL configuration

**Phase 3: Target Provisioning and Joining** (Weeks 6-8)

- Target cluster creation
- Learner join logic
- Data synchronization monitoring

**Phase 4: Promotion and Cleanup** (Weeks 9-10)

- Promotion gate logic
- Learner-to-member promotion
- Source member removal

**Phase 5: Observability and DR** (Weeks 11-12)

- Prometheus metrics
- Structured logging
- Automatic rollback

**Phase 6: Testing and Documentation** (Weeks 13-14)

- Unit tests (>80% coverage)
- Integration tests (envtest)
- E2E tests
- User documentation

**Phase 7: Alpha Release** (Week 15)

- Feature gate for migration controller
- Alpha documentation
- Early feedback collection

### Testing Strategy

**Unit Tests:**

- Controller reconciliation logic
- State machine transitions
- Validation rules

**Integration Tests (envtest):**

- End-to-end migration workflow
- Rollback scenarios
- Failure injection

**E2E Tests:**

- Real cluster migration
- Performance benchmarks
- Cross-cloud scenarios

## Alternatives

### Alternative 1: Snapshot and Restore (Current Approach)

**Description:** Take snapshot, stop API servers, restore, restart.

**Rejected because:**

- ❌ Requires downtime
- ❌ High operational risk
- ❌ No gradual rollout

### Alternative 2: Use Only External URLs

**Description:** Configure all members with only external URLs (no internal DNS).

**Rejected because:**

- ❌ Forces all intra-cluster traffic through load balancers
- ❌ Higher cloud costs for LB traffic
- ❌ Additional latency for local communication

### Alternative 3: DNS Manipulation

**Description:** Use external DNS to make clusters reachable.

**Rejected because:**

- ❌ Requires external DNS infrastructure
- ❌ Not portable across cloud providers
- ❌ Security concerns (DNS spoofing)

### Alternative 4: Network Mesh/Overlay

**Description:** Use service mesh for cross-cluster connectivity.

**Rejected because:**

- ❌ Additional infrastructure complexity
- ❌ Performance overhead
- ❌ Vendor lock-in concerns

## Benefits

✅ **Zero-Downtime Migration**: No API server restarts required  
✅ **Cloud-Agnostic**: Works across any cloud provider or on-premises  
✅ **Safe and Reversible**: Automatic snapshots and rollback  
✅ **Observable**: Comprehensive metrics and logging  
✅ **CNCF-Compliant**: Follows Kubernetes operator best practices  
✅ **Flexible**: Manual or automatic promotion  

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Network partitions during migration | Health checks between phases; automatic rollback |
| Data inconsistency | Promotion gates ensure sync before promotion |
| Certificate expiration | Integration with cert-manager; monitoring alerts |
| Quorum loss | Validation prevents unsafe operations |
| Failed rollback | Pre-migration snapshot as ultimate safety net |

## Migration Path

For existing users of `bootstrapWithExistingCluster` (if any):

1. Introduce `EtcdMigration` CRD alongside existing approach
2. Add migration controller with backward compatibility
3. Deprecate direct bootstrap approach
4. Remove deprecated approach in v2.0

## Success Criteria

- [ ] Zero-downtime migration demonstrated in test environments
- [ ] >80% unit test coverage
- [ ] Complete integration and E2E test suites
- [ ] Prometheus metrics and alerts implemented
- [ ] Documentation and runbooks completed
- [ ] Alpha release with feature gate
- [ ] Positive feedback from early adopters

## References

- [CNCF Etcd Migration Best Practices](https://etcd.io/docs/v3.5/op-guide/recovery/)
- [Kubernetes Operator Pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [Etcd Learner Design](https://etcd.io/docs/v3.5/learning/design-learner/)
- etcd-druid DEP-05: [Etcd Operator Tasks](05-etcd-operator-tasks.md)

## Appendix

### Example Migration Resource

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: EtcdMigration
metadata:
  name: production-cpm-us-to-eu
  namespace: garden
spec:
  sourceEtcdRef:
    name: etcd-main-us-east
    namespace: garden
  
  targetEtcdSpec:
    replicas: 3
    backup:
      store: s3
      s3:
        bucket: etcd-backup-eu-west
        region: eu-west-1
  
  strategy:
    type: LiveMigration
    liveMigration:
      autoProgression: false
      promotionReadiness:
        minSyncDuration: 10m
        maxLagBytes: 524288  # 512KB
      rollbackPolicy:
        automatic: true
        conditions:
          - type: SyncTimeout
            duration: 1h
  
  networking:
    sourceExternalEndpoints:
      peerURLs:
        - name: etcd-main-us-east-0
          urls: ["https://us-etcd-0.example.com:2380"]
        - name: etcd-main-us-east-1
          urls: ["https://us-etcd-1.example.com:2380"]
        - name: etcd-main-us-east-2
          urls: ["https://us-etcd-2.example.com:2380"]
      clientURL: "https://us-etcd-client.example.com:2379"
    
    targetExternalEndpoints:
      peerURLs:
        - name: etcd-main-eu-west-0
          urls: ["https://eu-etcd-0.example.com:2380"]
        - name: etcd-main-eu-west-1
          urls: ["https://eu-etcd-1.example.com:2380"]
        - name: etcd-main-eu-west-2
          urls: ["https://eu-etcd-2.example.com:2380"]
```

### Example Operator Command Flow

```bash
# Step 1: Create external LB services (one-time setup)
kubectl apply -f external-lb-services.yaml

# Step 2: Create migration resource
kubectl apply -f migration.yaml

# Step 3: Monitor progress
kubectl get etcdmigration production-cpm-us-to-eu -w

# Step 4: Check readiness
kubectl get etcdmigration production-cpm-us-to-eu \
  -o jsonpath='{.status.conditions[?(@.type=="ReadyForPromotion")].status}'

# Step 5: Approve promotion
kubectl annotate etcdmigration production-cpm-us-to-eu \
  druid.gardener.cloud/promote=true

# Step 6: Verify completion
kubectl get etcdmigration production-cpm-us-to-eu \
  -o jsonpath='{.status.phase}'
```
