# Phase 11: Zero-Manual-Step Local Deploy with etcd-steward

**Date:** 2026-04-03  
**Goal:** `make kind-up` + `make deploy-steward-dev STEWARD_DIR=<path>` + `kubectl apply etcd.yaml` → 2/2 Running, BackupReady=True. No `kind load`, no `kubectl annotate`, no manual patches.

---

## Issue

After Phases 9–10, the Local provider and EtcdMember lifecycle work automatically. The remaining friction is that etcd-steward image deployment requires two separate manual commands:

```bash
# Manual step A — separate from deploy
./hack/build-steward.sh /path/to/etcd-steward   # builds + kind load

# Manual step B — separate from make deploy
./hack/deploy-local.sh run -p etcd-steward-dev
```

`make deploy` does NOT include the `etcd-steward-dev` skaffold profile.

## Root Cause

`build-steward.sh` uses `kind load docker-image` to put the image into the KinD node's containerd. This works but is:
- A separate step from `make deploy`
- Not composable with skaffold deploy

The local registry (`localhost:5001`) is already set up by `make kind-up` and accessible from inside the KinD cluster. Pushing to it eliminates the `kind load` step entirely.

## Design Summary

### Approach: Push to local registry + single make target

1. `build-steward.sh`: push to `localhost:5001/etcd-steward:local` instead of `kind load`
2. `values-steward-dev.yaml`: reference `localhost:5001/etcd-steward` instead of bare `etcd-steward`
3. `Makefile`: add `deploy-steward-dev` target that calls build-steward.sh + deploy-local.sh in sequence

Result: one command does everything.

## Tasks

- [ ] **Task 1**: Update `build-steward.sh` to push to local registry instead of `kind load`  
  **Acceptance:** builds binary, builds image, pushes to `localhost:5001/etcd-steward:local`; no `kind load`  
  **Files:** `hack/build-steward.sh`

- [ ] **Task 2**: Update `values-steward-dev.yaml` to use registry image URL  
  **Acceptance:** `imageVectorOverwrite.images[0].repository = "localhost:5001/etcd-steward"`  
  **Files:** `charts/values-steward-dev.yaml`

- [ ] **Task 3**: Add `make deploy-steward-dev` Makefile target  
  **Acceptance:** `make deploy-steward-dev STEWARD_DIR=<path>` runs T1+T2 in sequence; no other manual steps  
  **Files:** `Makefile`

- [ ] **Task 4**: E2E validation — fresh cluster, one command  
  **Acceptance:** recreate cluster, `make deploy-steward-dev STEWARD_DIR=<path>`, `kubectl apply etcd.yaml` → `2/2 Running`, `BackupReady=True`. Zero `kubectl annotate`, `kind load`, `docker exec`.

## Testing Strategy

- T1: `./hack/build-steward.sh <dir>` → image at `localhost:5001/etcd-steward:local`; `docker pull localhost:5001/etcd-steward:local` succeeds
- T2: `helm template . --set-file imageVectorOverwrite=...` includes correct repo URL
- T3: `make deploy-steward-dev STEWARD_DIR=<dir>` completes; `kubectl get deploy etcd-druid` → `1/1 Ready`
- T4: Manual KinD E2E

## Rollback

- `build-steward.sh`: restore `kind load` call (additive change only)
- `values-steward-dev.yaml`: revert repository to `etcd-steward`
- `Makefile`: remove `deploy-steward-dev` target
