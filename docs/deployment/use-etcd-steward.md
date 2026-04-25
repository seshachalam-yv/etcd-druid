---
title: "UseEtcdSteward: Migration Guide"
---

# UseEtcdSteward: Migration Guide

This document describes how to enable the `UseEtcdSteward` feature gate, what changes it introduces, how to verify correct operation, and how to roll back if needed.

## Overview

The `UseEtcdSteward` feature gate replaces [etcd-backup-restore](https://github.com/gardener/etcd-backup-restore) with [etcd-steward](https://github.com/gardener/etcd-steward) as the sidecar container for etcd clusters managed by etcd-druid. When enabled, etcd-druid:

1. Deploys the **etcd-steward** image in the backup-restore container slot.
2. Creates **`EtcdMember`** custom resources to track per-member lifecycle state, replacing the lease-based status reporting used by etcd-backup-restore.
3. Skips creation of **snapshot leases** (full and delta), since snapshot status moves to `EtcdMember.Status.Snapshots`.
4. Configures the **etcd-wrapper** readiness probe to hit steward's `/healthz` endpoint over plain HTTP instead of the backup-restore HTTPS endpoint.

!!! note
    This feature is currently in **Alpha** stage (disabled by default). It is recommended for use only in development and testing environments.

## Prerequisites

Before enabling `UseEtcdSteward`, ensure the following:

1. **etcd-steward image is available.** The etcd-steward container image must be accessible from your cluster. The image is referenced in etcd-druid's [image vector](https://github.com/gardener/etcd-druid/blob/master/internal/images/images.yaml) under the key `etcd-steward`. If you are using a private registry, ensure the image is mirrored or an `IMAGEVECTOR_OVERWRITE` file is configured.

2. **etcd-druid version >= v0.37.0.** The `UseEtcdSteward` feature gate and `EtcdMember` CRD were introduced in v0.37.0.

3. **EtcdMember CRD is installed.** The CRD is included in the etcd-druid Helm chart under `charts/crds/`. If you install etcd-druid via Helm with `--include-crds`, the CRD is installed automatically. If you manage CRDs separately, apply `druid.gardener.cloud_etcdmembers.yaml` before enabling the feature gate.

4. **etcd-wrapper compatibility.** The etcd-wrapper image (v0.6.2+) already supports the steward contract. When `UseEtcdSteward` is enabled, etcd-druid passes `--backup-restore-tls-enabled=false` to the wrapper, which causes it to probe the sidecar over HTTP.

## Enabling the Feature

### Via Helm Values

Set the feature gate in your etcd-druid Helm values file:

```yaml
featureGates:
  UseEtcdSteward: true
```

Then deploy or upgrade etcd-druid:

```bash
helm upgrade etcd-druid charts/ -f values.yaml
```

### Via Operator Configuration

If you use the `--config` CLI flag with an [OperatorConfiguration](https://github.com/gardener/etcd-druid/blob/master/api/config/v1alpha1/types.go) file, set the feature gate in the configuration:

```yaml
apiVersion: druid.gardener.cloud/v1alpha1
kind: OperatorConfiguration
featureGates:
  UseEtcdSteward: true
```

### Via CLI Flag (Deprecated)

```bash
etcd-druid --feature-gates=UseEtcdSteward=true
```

## Verification

After enabling the feature gate and restarting etcd-druid, verify the following for each `Etcd` resource:

### 1. Sidecar Container Image

The StatefulSet should use the etcd-steward image in the `backup-restore` container:

```bash
kubectl get sts <etcd-name> -n <namespace> -o jsonpath='{.spec.template.spec.containers[?(@.name=="backup-restore")].image}'
# Expected: europe-docker.pkg.dev/gardener-project/public/gardener/etcd-steward:<version>
```

### 2. EtcdMember Custom Resources

An `EtcdMember` CR should exist for each pod/replica:

```bash
kubectl get etcdmembers -n <namespace> -l gardener.cloud/owned-by=<etcd-name>
# Expected: one EtcdMember per replica (e.g., etcd-main-0 for a 1-replica cluster)
```

### 3. EtcdMember Status

Each `EtcdMember` should eventually report its lifecycle state:

```bash
kubectl get etcdmember <etcd-name>-0 -n <namespace> -o yaml
# Expected status fields:
#   state: Started
#   subState: Leader (for single-node) or Follower/Leader (for multi-node)
```

### 4. Snapshot Leases Absent

Snapshot leases should not be created when steward is active:

```bash
kubectl get lease -n <namespace> -l app.kubernetes.io/managed-by=etcd-druid,app.kubernetes.io/component=etcd-snapshot-lease
# Expected: No resources found (or only pre-existing leases from before the switch)
```

### 5. Etcd Cluster Health

The `Etcd` resource should report healthy status:

```bash
kubectl get etcd <etcd-name> -n <namespace> -o wide
# Expected: READY=true, QUORATE=True, ALL MEMBERS READY=True
```

### 6. Backup Health (if backup store is configured)

Trigger a full snapshot and verify it completes:

```bash
# Create an EtcdOpsTask to trigger a full snapshot, or check the EtcdMember status:
kubectl get etcdmember <etcd-name>-0 -n <namespace> -o jsonpath='{.status.snapshots.lastFull}'
```

## What Changes When the Gate Is Enabled

The following table summarizes the behavioral differences:

| Aspect | `UseEtcdSteward=false` (default) | `UseEtcdSteward=true` |
|--------|----------------------------------|----------------------|
| Sidecar image | etcd-backup-restore | etcd-steward |
| Sidecar protocol | HTTPS with TLS | HTTP (plain) |
| Readiness probe | HTTPS GET to backup-restore | HTTP GET to steward `/healthz` |
| Member status source | Member leases (`Spec.HolderIdentity`) | `EtcdMember.Status` custom resources |
| Snapshot status source | Snapshot leases (full + delta) | `EtcdMember.Status.Snapshots` |
| Snapshot leases | Created and maintained | Not created (skipped) |
| Snapshot trigger (compaction) | HTTPS GET to backup-restore | HTTP POST to steward `/snapshot/full` |
| EtcdMember CRs | Not created | Created per replica |
| Wrapper TLS flag | `--backup-restore-tls-enabled=true` | `--backup-restore-tls-enabled=false` |

## Rollback Procedure

The `UseEtcdSteward` feature gate supports bidirectional toggling. Rolling back from etcd-steward to etcd-backup-restore has been verified (see [toggle test results](../proposals/08-etcd-steward-rollout-plan.md#appendix-d-feature-gate-toggle-test--verified-2026-04-23)).

### Steps

1. **Disable the feature gate** in your Helm values or operator configuration:

    ```yaml
    featureGates:
      UseEtcdSteward: false
    ```

2. **Redeploy etcd-druid:**

    ```bash
    helm upgrade etcd-druid charts/ -f values.yaml
    ```

3. **Trigger reconciliation** of affected `Etcd` resources (or wait for the periodic reconciliation):

    ```bash
    kubectl annotate etcd <etcd-name> -n <namespace> gardener.cloud/operation=reconcile
    ```

4. **Verify rollback:**

    ```bash
    # Sidecar should now be etcd-backup-restore
    kubectl get sts <etcd-name> -n <namespace> -o jsonpath='{.spec.template.spec.containers[?(@.name=="backup-restore")].image}'

    # Etcd should be healthy
    kubectl get etcd <etcd-name> -n <namespace> -o wide
    ```

### Rollback Characteristics

- **No data loss.** Snapshot format is identical between etcd-steward and etcd-backup-restore (same snapstore library). A snapshot taken by steward can be restored by backup-restore and vice versa.
- **No snapshot re-creation needed.** Existing snapshots in the object store remain valid.
- **Brief downtime.** Each etcd StatefulSet performs a rolling update to switch the sidecar container. For a single-member cluster, expect approximately 40 seconds of downtime. For multi-member clusters, pods update one at a time.
- **EtcdMember CRs become orphans.** When the gate is disabled, no controller reads the `EtcdMember` resources. They can be cleaned up manually or left in place for a future re-enablement.
- **Snapshot leases are re-created.** The snapshot lease component re-creates leases on the next reconciliation cycle.

## Known Limitations

The following limitations apply during the Alpha stage:

| Limitation | Impact | Tracking |
|-----------|--------|----------|
| Compaction Job still uses etcd-backup-restore image | The snapshot compaction Job has not yet been migrated to use etcd-steward's `compact` subcommand. This may cause some compaction-related E2E test failures with the `Local` backup provider. | Blocks Beta graduation |
| Steward HTTP server does not support TLS | In environments that require TLS on all sidecar endpoints, the steward's plain HTTP server may not meet security policies. | Blocks Beta for TLS-strict environments |
| Defrag not scheduled | The `EtcdMember.Status.LastDefragmentation` field exists in the CRD but etcd-steward's defrag scheduling is not yet enabled. The `/defrag` endpoint is wired but not periodically triggered. | P2 follow-up |
| BackupReady condition wiring | The `BackupReady` condition in `Etcd.Status` may not be correctly determined when using steward, since the health checker reads from snapshot leases which are not created with steward. | Blocks Beta graduation |

## Compatibility Notes

- **Toggle test verified:** Switching between `UseEtcdSteward=true` and `UseEtcdSteward=false` has been tested and verified to work without data loss. See [Appendix D of the rollout plan](../proposals/08-etcd-steward-rollout-plan.md#appendix-d-feature-gate-toggle-test--verified-2026-04-23).
- **Snapshot format compatibility:** etcd-steward uses the same snapshot format and snapstore library as etcd-backup-restore. Cross-sidecar snapshot restore is supported.
- **Multi-cluster coexistence:** During the transition, different Gardener seeds can run different etcd-druid configurations. The feature gate is per etcd-druid instance, not per `Etcd` resource.
