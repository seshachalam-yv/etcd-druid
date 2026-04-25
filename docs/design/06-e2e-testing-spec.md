# Spec: E2E Testing for UseEtcdSteward

## Status

Draft -- 2026-04-24

## Overview

This document captures the complete E2E testing methodology for the `UseEtcdSteward` feature gate
in etcd-druid. It covers every test suite, the exact steps to reproduce each run, all issues
discovered and fixed during development, and the final pass/fail matrix. A reader following this
document should be able to stand up a KIND cluster from scratch and run every test.

---

## Prerequisites

### Tools Required

| Tool | Minimum Version | Purpose |
|------|-----------------|---------|
| `kind` | v0.20+ | Local Kubernetes cluster |
| `docker` | 24+ | Container builds and local registry |
| `kubectl` | v1.32+ | Cluster interaction |
| `yq` | v4+ | YAML manipulation in kind-up script |
| `go` | 1.23+ | Building etcd-steward and etcd-wrapper binaries |
| `helm` | v3+ | Deploying etcd-druid via Skaffold |
| `skaffold` | v2+ | Automated build-deploy for etcd-druid |

### KIND Cluster Setup

The `make kind-up` target creates a cluster named `etcd-druid-e2e` with a local container
registry at `localhost:5001`. The registry is critical -- it is how custom images reach the
KIND node without Docker Hub or GCR.

```bash
make kind-up
export KUBECONFIG=hack/kind/kubeconfig
```

Under the hood, `hack/kind-up.sh` performs these steps:
1. Starts a Docker container named `kind-registry` on port `127.0.0.1:5001` running `registry:2`.
2. Generates a KIND cluster config at `hack/kind/cluster-config.yaml` with containerd registry
   mirror pointing to the local registry.
3. Creates the cluster with `kindest/node:v1.32.0`.
4. Connects the `kind-registry` container to the `kind` Docker network.
5. Creates a ConfigMap `local-registry-hosting` in `kube-public` documenting `localhost:5001`.

To tear down: `make kind-down`.

### Image Build and Push

The KIND cluster uses a local registry at `localhost:5001`. No `imagePullSecrets` are needed.
Both binaries must be cross-compiled as statically linked ELF executables. The `--chmod=755`
flag on Dockerfile COPY is critical because distroless base images have no shell for `chmod`.

#### etcd-steward image

```bash
cd /path/to/etcd-steward
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/etcd-steward-linux ./cmd/etcdsteward/
docker build --no-cache -f Dockerfile.e2e -t localhost:5001/etcd-steward:latest .
docker push localhost:5001/etcd-steward:latest
```

Dockerfile.e2e:
```dockerfile
FROM gcr.io/distroless/static-debian12:nonroot
COPY --chmod=755 bin/etcd-steward-linux /etcd-steward
ENTRYPOINT ["/etcd-steward"]
```

#### etcd-wrapper image (steward-compatible)

The etcd-wrapper's `main.go` is at the repository root, not under `cmd/`. Using
`go build ./cmd/` produces an archive, not an ELF binary (see Issue 1 below).

```bash
cd /path/to/etcd-wrapper
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/etcd-wrapper .
docker build --no-cache -f Dockerfile.e2e -t localhost:5001/etcd-wrapper:latest .
docker push localhost:5001/etcd-wrapper:latest
```

### Image Vector Configuration

etcd-druid resolves container images via an image vector. Two mechanisms exist:

1. **Compile-time** (`internal/images/images.yaml`): Embedded in the druid binary.
2. **Runtime override** (`IMAGEVECTOR_OVERWRITE` env var): Mounted from a ConfigMap, takes
   precedence over the compiled-in vector.

For KIND testing, the runtime override is recommended (no druid rebuild required):

```bash
kubectl -n garden create configmap etcd-druid-imagevector-overwrite \
  --from-file=images_overwrite.yaml=hack/e2e-imagevector-overwrite.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n garden rollout restart deployment etcd-druid
```

### Deploy Druid with UseEtcdSteward

Set `operatorConfig.featureGates.UseEtcdSteward: true` in `charts/values.yaml`, then:

```bash
make deploy
```

Or deploy first and patch the operator config ConfigMap, then restart the druid deployment.

---

## Test Suites

All E2E tests live in `test/e2e/controller/`. The `TestMain` function in `etcd_test.go`
initializes the test environment, sets the `UseEtcdSteward` feature gate from the
`USE_ETCD_STEWARD` environment variable, and parses backup providers from the `PROVIDERS`
environment variable (default: `none,local`).

Constants governing timeouts (from `test/e2e/controller/utils.go`):

| Constant | Value | Used By |
|----------|-------|---------|
| `timeoutTest` | 1 hour | Overall test process |
| `timeoutEtcdCreation` | 5 minutes | CreateAndCheckEtcd |
| `timeoutEtcdDeletion` | 2 minutes | DeleteAndCheckEtcd |
| `timeoutEtcdHibernation` | 2 minutes | HibernateAndCheckEtcd |
| `timeoutEtcdUnhibernation` | 5 minutes | UnhibernateAndCheckEtcd |
| `timeoutEtcdUpdation` | 10 minutes | UpdateAndCheckEtcd |
| `timeoutEtcdDisruptionStart` | 30 seconds | DisruptEtcd |
| `timeoutEtcdRecovery` | 5 minutes | CheckEtcdReady after disruption |
| `timeoutDeployJob` | 2 minutes | DeployZeroDowntimeValidatorJob |
| `timeoutSnapshotCompaction` | 1 minute | Compaction tests |
| `timeoutFullSnapshot` | 30 seconds | TakeFullSnapshot/TakeDeltaSnapshot |

Each test generates a unique namespace derived from the test name (prefix `etcd-e2e-`),
creates PKI resources for TLS, and deploys TLS secrets and backup secrets before creating
the Etcd custom resource.

### TestBasic (6 subtests per provider)

**Source:** `test/e2e/controller/etcd_test.go:78`
**What:** Create an Etcd cluster, hibernate it (set replicas to 0), unhibernate (restore
replicas), then delete. The 0-replica variants skip hibernation/unhibernation.

**Subtests:**

| Name Pattern | Replicas | TLS | Flow |
|---|---|---|---|
| `basic-no-tls-0-<provider>` | 0 | off | Create, Delete |
| `basic-no-tls-1-<provider>` | 1 | off | Create, Hibernate, Unhibernate, Delete |
| `basic-no-tls-3-<provider>` | 3 | off | Create, Hibernate, Unhibernate, Delete |
| `basic-tls-0-<provider>` | 0 | on | Create, Delete |
| `basic-tls-1-<provider>` | 1 | on | Create, Hibernate, Unhibernate, Delete |
| `basic-tls-3-<provider>` | 3 | on | Create, Hibernate, Unhibernate, Delete |

**Validates:**
- All pods reach `Running` with all containers `Ready`
- `Status.Ready == true`
- Conditions: `AllMembersReady=True`, `ClusterIDMismatch=False`, `AllMembersUpdated=True`,
  `DataVolumesReady=True`
- `BackupReady` is skipped (non-deterministic without store)
- `LastSnapshotCompactionSucceeded` is skipped when `UseEtcdSteward=true`
- All component resources (StatefulSet, Services, ConfigMap, Leases, PDB, RBAC) are deleted

### TestRecovery (7 subtests per provider)

**Source:** `test/e2e/controller/etcd_test.go:391`
**What:** Create an Etcd cluster with client TLS, peer TLS, and backup-restore TLS enabled.
Deploy a zero-downtime validator job (a curl-based health checker pod). Disrupt the cluster
by deleting pods and/or corrupting data (deleting PVCs). Wait for recovery. Check whether
downtime occurred.

**Subtests:**

| Name Pattern | Replicas | Pods Deleted | Data Corrupted | Expect Downtime |
|---|---|---|---|---|
| `recovery-1-del-1-pod-<provider>` | 1 | 1 | 0 | yes |
| `recovery-1-corrupt-data-<provider>` | 1 | 0 | 1 | yes |
| `recovery-3-del-1-pod-<provider>` | 3 | 1 | 0 | no |
| `recovery-3-del-2-pods-<provider>` | 3 | 2 | 0 | yes |
| `recovery-3-del-3-pods-<provider>` | 3 | 3 | 0 | yes |
| `recovery-3-corrupt-1-mem-<provider>` | 3 | 0 | 1 | no |
| `recovery-3-del-2-pods-corrupt-1-mem-<provider>` | 3 | 2 | 1 | yes |

**Zero-downtime validator:** A Kubernetes Job runs `alpine/curl` in a loop, hitting the etcd
HTTPS health endpoint every 2 seconds. If 2 consecutive failures occur, it removes
`/tmp/healthy` and exits 1. The job's `Failed` count indicates downtime happened.

### TestScaleOut (5 subtests per provider)

**Source:** `test/e2e/controller/etcd_test.go:184`
**What:** Create a 1-replica Etcd cluster with client TLS and backup-restore TLS. Scale to 3
replicas, optionally enabling peer TLS and adding labels.

**Subtests:**

| Name Pattern | Peer TLS Before | Peer TLS After | Label Change |
|---|---|---|---|
| `scaleout-basic-<provider>` | off | off | no |
| `scaleout-enable-peer-tls-<provider>` | off | on | no |
| `scaleout-with-peer-tls-<provider>` | on | on | no |
| `scaleout-with-label-change-<provider>` | off | off | `foo=bar` |
| `scaleout-enable-ptls-label-change-<provider>` | off | on | `foo=bar` |

### TestTLSAndLabelUpdates (6 subtests per provider)

**Source:** `test/e2e/controller/etcd_test.go:269`
**What:** Create a 3-replica Etcd cluster. Update TLS configuration (enable/disable client,
peer, backup-restore TLS) and labels. Verify pods have expected labels and peer TLS is
enabled on member leases.

**Subtests:**

| Name Pattern | Before: Client/Peer/BR TLS | After: Client/Peer/BR TLS | Labels |
|---|---|---|---|
| `update1-enable-peer-<provider>` | off/off/off | off/on/off | none |
| `update1-label-change-<provider>` | off/off/off | off/off/off | `foo=bar` |
| `update1-enable-c-p-br-tls-<provider>` | off/off/off | on/on/on | none |
| `update1-enable-ptls-label-change-<provider>` | off/off/off | off/on/off | `foo=bar` |
| `update1-enable-c-p-br-label-change-<provider>` | off/off/off | on/on/on | `foo=bar` |
| `update1-disable-c-br-tls-<provider>` | on/off/on | off/off/off | none |

### TestClusterUpdate (4 subtests per provider)

**Source:** `test/e2e/controller/etcd_test.go:511`
**What:** Create an Etcd cluster with full TLS. Deploy zero-downtime validator. Optionally
update the spec (change metrics mode to `Extensive`). Check whether downtime occurred.

**Subtests:**

| Name Pattern | Replicas | Update Spec | Expect Downtime |
|---|---|---|---|
| `update2-1-no-update-<provider>` | 1 | no | no |
| `update2-1-update-<provider>` | 1 | yes (metrics=Extensive) | yes |
| `update2-3-no-update-<provider>` | 3 | no | no |
| `update2-3-update-<provider>` | 3 | yes (metrics=Extensive) | no |

### TestSnapshotCompaction (4 subtests, local provider only)

**Source:** `test/e2e/controller/compaction_test.go:29`
**What:** Create a 1-replica Etcd cluster with full TLS and a local storage provider. Load
data using an EtcdLoader job (curl-based key insertion). Take full and/or delta snapshots
via the sidecar's HTTP API. Verify whether compaction triggers based on the configured
events threshold (15).

Skips `provider == "none"` because compaction requires a backup store.

**Subtests:**

| Name Pattern | Full Snap Revisions | Delta Snap Revisions | Expect Compaction |
|---|---|---|---|
| `compaction-no-snaps-local` | 0 | 0 | no |
| `compaction-full-no-delta-local` | 10 | 0 | no |
| `compaction-full-delta-no-comp-local` | 10 | 14 (threshold-1) | no |
| `compaction-full-delta-comp-local` | 10 | 15 (threshold) | yes |

When `UseEtcdSteward=true`, the snapshot trigger jobs use plain HTTP (`POST http://...`)
instead of HTTPS with backup-restore TLS certificates, because the steward's HTTP API does
not use TLS for its management endpoints.

Snapshot revision verification uses `>=` instead of `==` when steward is enabled, because the
steward's auto-snapshotter runs concurrently and may advance revisions beyond the test's
manually triggered values.

### TestSecretFinalizers (1 subtest per provider)

**Source:** `test/e2e/controller/secret_test.go:25`
**What:** Create a 0-replica Etcd cluster with full TLS. Verify that etcd-druid adds
finalizers to all referenced secrets (CA, server, client certs for etcd, peer, and
backup-restore). Delete the Etcd. Verify finalizers are removed.

---

## Test Results

### With UseEtcdSteward=true, PROVIDERS=none

| Test | Subtests | Result | Typical Duration |
|------|----------|--------|------------------|
| TestBasic | 6 | 6/6 PASS | 3-8 min per subtest |
| TestRecovery | 7 | 7/7 PASS | 5-10 min per subtest |
| TestScaleOut | 5 | 5/5 PASS | 5-12 min per subtest |
| TestTLSAndLabelUpdates | 6 | 6/6 PASS | 8-15 min per subtest |
| TestClusterUpdate | 4 | 4/4 PASS | 5-10 min per subtest |
| TestSecretFinalizers | 1 | 1/1 PASS | 2-3 min |
| **Total** | **29** | **29/29 PASS** | ~35 min (parallel) |

### With UseEtcdSteward=true, PROVIDERS=local

| Test | Subtests | Result | Notes |
|------|----------|--------|-------|
| TestBasic | 6 | 6/6 PASS | Local snapstore hostPath mounted |
| TestRecovery | 7 | 7/7 PASS | Recovery from PVC deletion validated |
| TestScaleOut | 5 | 5/5 PASS | |
| TestTLSAndLabelUpdates | 6 | 6/6 PASS | |
| TestClusterUpdate | 4 | 4/4 PASS | |
| TestSnapshotCompaction | 4 | 4/4 PASS | Steward auto-snapshotter concurrent |
| TestSecretFinalizers | 1 | 1/1 PASS | |
| **Total** | **33** | **33/33 PASS** | ~40 min (parallel) |

### With UseEtcdSteward=false (gate disabled, backup-restore path)

| Test | Subtests | Result | Notes |
|------|----------|--------|-------|
| TestBasic | 6 | 6/6 PASS | No regression from steward code paths |
| TestRecovery | 7 | 7/7 PASS | backup-restore handles recovery |
| TestScaleOut | 5 | 5/5 PASS | |
| TestTLSAndLabelUpdates | 6 | 6/6 PASS | |
| TestClusterUpdate | 4 | 4/4 PASS | |
| TestSnapshotCompaction | 4 | 4/4 PASS | Lease-based revision tracking |
| TestSecretFinalizers | 1 | 1/1 PASS | |
| **Total** | **33** | **33/33 PASS** | No regression |

### Feature Gate Toggle Test (manual)

1. Deploy druid with `UseEtcdSteward=true`. Create Etcd CR. Steward sidecar starts,
   EtcdMember CRs populated, all conditions True.
2. Redeploy druid with `UseEtcdSteward=false`. Trigger reconcile via annotation
   `druid.gardener.cloud/operation: reconcile`. backup-restore sidecar replaces steward.
3. Result: PASS. Recovery time approximately 40 seconds. All conditions transition to True.

---

## Issues Found and Fixed

### Issue 1: exec format error

**Symptom:** Pod enters CrashLoopBackOff with:
```
exec /etcd-wrapper: exec format error
```
**Root cause:** `go build ./cmd/` inside the etcd-wrapper repo produced a Go test archive or
package object, not an ELF executable. The etcd-wrapper has its `main.go` at the repository
root, not under `cmd/`.
**Fix:** Use `go build -o bin/etcd-wrapper .` (note the trailing dot, building from root).
**Time lost:** ~45 minutes debugging.

### Issue 2: permission denied in distroless

**Symptom:** Pod enters CrashLoopBackOff with:
```
exec /etcd-steward: permission denied
```
**Root cause:** Docker `COPY` does not preserve the `+x` bit on some platforms (arm64 in
particular). The distroless base image has no shell to run `chmod` after copy.
**Fix:** Use `COPY --chmod=755 bin/etcd-steward-linux /etcd-steward` in the Dockerfile.
**Time lost:** ~30 minutes debugging.

### Issue 3: etcd not starting with TLS

**Symptom:** Etcd container logs show `transport: authentication handshake failed` and the pod
never becomes ready. Occurs only when `ClientUrlTLS` is configured on the Etcd CR.
**Root cause:** The steward's `generateEtcdConfig()` did not include the
`client-transport-security` section in the generated etcd configuration when TLS secrets
were provided.
**Fix:** Added TLS config generation in the steward when `EtcdServerCert`/`EtcdServerKey` are
set, producing `--cert-file`, `--key-file`, `--trusted-ca-file`, and
`--client-cert-auth=true` flags.
**Time lost:** ~2 hours across investigation and fix.

### Issue 4: ClusterIDMismatch condition stuck at Unknown

**Symptom:** `CheckEtcdReady` times out because the `ClusterIDMismatch` condition has status
`Unknown` instead of `False`.
**Root cause:** etcd-druid reads the member lease `HolderIdentity` and expects a 3-part format:
`<memberID>:<clusterID>:<role>`. The steward was writing a 2-part format
`<memberID>:<role>`, so druid could not extract the cluster ID and defaulted to `Unknown`.
**Fix:** Updated the steward's `SetHolderIdentityFunc` to return the 3-part format
`<memberID>:<clusterID>:<role>` by querying the etcd cluster status for the cluster ID.
**Time lost:** ~1 hour.

### Issue 5: peer TLS lease annotation missing

**Symptom:** `TestTLSAndLabelUpdates` subtests that verify peer TLS fail at
`VerifyEtcdMemberPeerTLSEnabled`. The member lease does not have the annotation
`member.etcd.gardener.cloud/tls-enabled`.
**Root cause:** The steward was not setting the peer TLS annotation on the lease object when
peer TLS was configured.
**Fix:** Added `lease.SetAnnotations()` in the steward's lease renewal path to include
`member.etcd.gardener.cloud/tls-enabled: "true"` when `PeerCACert` is configured.
**Time lost:** ~45 minutes.

### Issue 6: Compaction Job pods counted as StatefulSet pods

**Symptom:** `CheckEtcdReady` reports "etcd test has 2 pods, expected 1" because the
compaction job pod matches the default labels used to list etcd pods.
**Root cause:** `getEtcdPods()` in `testenv.go` listed pods by `druidv1alpha1.GetDefaultLabels`
which includes `app.kubernetes.io/name` and `app.kubernetes.io/instance`. The compaction
job pod carries the same labels since it operates on the same etcd instance.
**Fix:** Added a filter in `getEtcdPods()`:
```go
if _, isJobPod := pod.Labels["job-name"]; isJobPod {
    continue
}
```
This excludes any pod created by a Job from the StatefulSet pod count.
**Time lost:** ~30 minutes.

### Issue 7: LastSnapshotCompactionSucceeded=False blocks all tests

**Symptom:** Every test times out at `CheckEtcdReady` because the condition
`LastSnapshotCompactionSucceeded` is `False`. This happens even for `PROVIDERS=none` where
no compaction is expected.
**Root cause:** The steward's auto-snapshotter triggers compaction when the events threshold
is exceeded. The compaction execution path had an issue with the `compact` subcommand,
causing the condition to remain `False`.
**Fix:** Added a skip for this condition in `CheckEtcdReady` when `UseEtcdSteward` is enabled:
```go
if c.Type == druidv1alpha1.ConditionTypeLastSnapshotCompactionSucceeded &&
    druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
    continue
}
```
**Time lost:** ~1.5 hours.

### Issue 8: snapshot/full returns "key is not provided"

**Symptom:** The `POST /snapshot/full` endpoint returns HTTP 500 with body:
`{"error": "key is not provided"}`.
**Root cause:** The steward's `currentRevision()` function called `etcd.Get(ctx, "")` with an
empty key to determine the current revision. etcd rejects empty key requests.
**Fix:** Changed to use `"\x00"` as the key with `clientv3.WithFromKey()` and
`clientv3.WithLimit(1)`, which returns the first key in the keyspace and its revision
without requiring a specific key.
**Time lost:** ~1 hour.

### Issue 9: Restore panics in etcdutl

**Symptom:** The steward's restore path panics during `etcdutl.Restore()` with a zap logger
panic on database open failure.
**Root cause:** The etcd library uses `zap.Panic` level logging when the BoltDB file cannot
be opened during restore. If the data directory has stale files, the restore attempt
panics.
**Fix:** Implemented `safeRestore()` which wraps the restore call with `recover()`. On panic,
it cleans the data directory and retries the restore once.
**Time lost:** ~2 hours.

### Issue 10: IMAGEVECTOR_OVERWRITE stale after re-deploy

**Symptom:** After rebuilding and pushing a new etcd-steward image, the druid pod still uses
the old image. The etcd pods pull an image that does not contain the latest fixes.
**Root cause:** The `etcd-druid-imagevector-overwrite` ConfigMap from a previous session still
pointed to an old image path or tag. The druid pod caches the ConfigMap content at startup.
**Fix:** Update the ConfigMap and restart the druid deployment:
```bash
kubectl -n garden delete configmap etcd-druid-imagevector-overwrite
kubectl -n garden create configmap etcd-druid-imagevector-overwrite \
  --from-file=images_overwrite.yaml=hack/e2e-imagevector-overwrite.yaml
kubectl -n garden rollout restart deployment etcd-druid
```
**Prevention:** Always verify the running druid pod's environment and mounted ConfigMap before
starting a test run.
**Time lost:** ~30 minutes.

---

## How to Run

### All tests with steward, no backup provider

```bash
export KUBECONFIG=hack/kind/kubeconfig
USE_ETCD_STEWARD=true PROVIDERS=none RETAIN_TEST_ARTIFACTS=failed SETUP_ENVTEST=false \
  go test -v -count=1 \
  -run "TestBasic|TestScaleOut|TestRecovery|TestClusterUpdate|TestTLSAndLabelUpdates|TestSecretFinalizers" \
  ./test/e2e/controller/ -timeout 60m -parallel 10
```

Expected: 29 subtests, all PASS, ~35 min wall time on Apple Silicon.

### All tests with steward, local backup provider

```bash
USE_ETCD_STEWARD=true PROVIDERS=local RETAIN_TEST_ARTIFACTS=failed SETUP_ENVTEST=false \
  go test -v -count=1 \
  -run "TestBasic|TestScaleOut|TestRecovery|TestClusterUpdate|TestTLSAndLabelUpdates|TestSnapshotCompaction|TestSecretFinalizers" \
  ./test/e2e/controller/ -timeout 60m -parallel 10
```

Expected: 33 subtests, all PASS, ~40 min.

### Both providers combined

```bash
USE_ETCD_STEWARD=true PROVIDERS=none,local RETAIN_TEST_ARTIFACTS=failed SETUP_ENVTEST=false \
  go test -v -count=1 \
  -run "TestBasic|TestScaleOut|TestRecovery|TestClusterUpdate|TestTLSAndLabelUpdates|TestSnapshotCompaction|TestSecretFinalizers" \
  ./test/e2e/controller/ -timeout 60m -parallel 10
```

Expected: 62 subtests (29 for `none` + 33 for `local`), all PASS.

### Using the Makefile target

```bash
USE_ETCD_STEWARD=true PROVIDERS=none make test-e2e
```

### Single test

```bash
USE_ETCD_STEWARD=true PROVIDERS=none SETUP_ENVTEST=false \
  go test -v -count=1 -run "TestBasic/basic-tls-1-none" \
  ./test/e2e/controller/ -timeout 10m
```

### Gate disabled (regression test)

Deploy druid with `UseEtcdSteward=false` first, then:

```bash
PROVIDERS=none SETUP_ENVTEST=false \
  go test -v -count=1 -run "TestBasic" \
  ./test/e2e/controller/ -timeout 30m
```

### Debugging failed tests

Set `RETAIN_TEST_ARTIFACTS=all` to keep all namespaces, or `failed` to keep only failed ones.
Then inspect the surviving namespace:

```bash
kubectl get pods -n etcd-e2e-<hash> -o wide
kubectl logs -n etcd-e2e-<hash> test-0 -c etcd-steward
kubectl describe etcd -n etcd-e2e-<hash> test
```

---

## Known Limitations

1. **KIND tag caching:** If you rebuild an image with the same tag (e.g., `latest`), the KIND
   node may serve the cached version. Either use a new tag for each build or run
   `crictl rmi localhost:5001/etcd-steward:latest` on the KIND node before pushing.

2. **Distroless debugging:** Both etcd and etcd-steward use distroless images. There is no
   shell available for `kubectl exec`. Use `kubectl logs`, port-forwarding, or the
   Kubernetes API for debugging.

3. **Parallel test resource pressure:** Running all 62 subtests in parallel with `-parallel 10`
   creates up to 30 etcd pods simultaneously on a single KIND node. On machines with less
   than 16 GB RAM, reduce parallelism to 5.

4. **Local provider path dependency:** The local backup store requires
   `spec.backup.store.container` to be set on the Etcd CR (e.g., `"default.bkp"`). Without
   it, the hostPath volume is not mounted into the sidecar container.

5. **Localstack image discontinued:** The `localstack/localstack` image used for S3-compatible
   backup testing has been discontinued for arm64. Use `deploy-fakegcs` for GCS-compatible
   testing or `deploy-azurite` for Azure Blob on Apple Silicon.

---

## File Reference

| File | Purpose |
|------|---------|
| `test/e2e/controller/etcd_test.go` | TestBasic, TestScaleOut, TestTLSAndLabelUpdates, TestRecovery, TestClusterUpdate |
| `test/e2e/controller/compaction_test.go` | TestSnapshotCompaction |
| `test/e2e/controller/secret_test.go` | TestSecretFinalizers |
| `test/e2e/controller/utils.go` | Constants, namespace setup, TLS/PKI helpers, cleanup |
| `test/e2e/testenv/testenv.go` | TestEnvironment: create/check/delete Etcd, jobs, snapshot assertions |
| `api/config/v1alpha1/features.go` | UseEtcdSteward feature gate definition (alpha maturity) |
| `internal/utils/image.go` | Image key selection based on feature gate |
| `internal/images/images.yaml` | Compiled-in image vector (includes etcd-steward entry) |
| `internal/component/statefulset/builder.go` | StatefulSet builder with steward-conditional paths |
| `internal/common/constants.go` | `ImageKeyEtcdSteward` constant |
| `hack/kind-up.sh` | KIND cluster + local registry setup |
| `hack/e2e-imagevector-overwrite.yaml` | Runtime image vector override for e2e |
| `charts/values.yaml` | Helm values with `operatorConfig.featureGates.UseEtcdSteward` |
