# Spec: etcd-wrapper Steward Compatibility Changes

## Status

Draft -- 2026-04-24

## Summary

This document specifies the changes to `etcd-wrapper` that enable it to work with
`etcd-steward` as a sidecar, while retaining full backward compatibility with the
existing `etcd-backup-restore` sidecar. The approach is a dual-mode race: both
initialization paths run concurrently and whichever delivers a valid etcd config
first wins. No CLI flags, environment variables, or image variants are required to
switch between modes.

## Problem

### Legacy Flow (etcd-backup-restore)

The etcd-wrapper was designed around a polling-based initialization protocol with
etcd-backup-restore:

1. Wrapper starts and polls `GET /initialization/status` on the backup-restore
   sidecar at `localhost:8080`.
2. If the status is `New`, wrapper triggers `POST /initialization/start` with a
   validation mode (full or sanity, based on the previous exit code).
3. Wrapper continues polling until the status becomes `Successful`.
4. Wrapper fetches the etcd config via `GET /config` from backup-restore.
5. Wrapper starts embedded etcd using `embed.StartEtcd(cfg)`.
6. Wrapper polls etcd readiness (a `GET` on a test key) every 2 seconds.
7. When etcd responds, `/readyz` returns HTTP 200.

The wrapper drives the entire lifecycle: it decides when to start initialization,
when to fetch config, and when to declare readiness.

### Steward Flow (etcd-steward)

With etcd-steward the flow is inverted. The steward owns initialization:

1. Steward starts its HTTP server and initialization logic.
2. Steward validates the data directory and restores from snapshot if needed.
3. Steward generates an etcd config (YAML) and `POST`s it to the wrapper at
   `POST /embedded-etcd`.
4. Steward waits for etcd to become ready (probing the etcd client endpoint).
5. Steward applies any delta operations (learner promotion, defragmentation).
6. Steward calls `POST /readyz/set` with body `ready` to mark the wrapper as
   ready for traffic.

The wrapper becomes a passive config receiver. It does not interact with the
sidecar at all; the sidecar pushes config and readiness state to the wrapper.

### Why This Matters

A hard switch between modes would require either a CLI flag or a container
environment variable to select the sidecar type. That creates a deployment-time
coupling and prevents zero-downtime migration from backup-restore to steward.
The dual-mode race eliminates this coupling entirely.

## Design Decision: Dual-Mode Race

Rather than hard-switching between modes, the wrapper races both initialization
paths concurrently inside `Setup()`:

- **Path A (legacy):** Run the existing `EtcdInitializer.Run()` which polls
  backup-restore's `/initialization/status`, triggers initialization, and
  fetches the config via `/config`.
- **Path B (steward):** Start the HTTP server early and poll an internal flag
  that is set when `POST /embedded-etcd` delivers a config.

Whichever path delivers a valid `*embed.Config` to the shared channel first wins.
The losing path is silently abandoned (its goroutine exits when the context is
cancelled or when the channel send falls through to the `default` case).

### How Mode Selection Works

| Sidecar present      | Path A (legacy)                  | Path B (steward)              | Winner  |
|----------------------|----------------------------------|-------------------------------|---------|
| etcd-backup-restore  | Polls successfully, returns cfg  | No POST arrives               | Path A  |
| etcd-steward         | Polls fail (no sidecar on 8080) | POST /embedded-etcd arrives   | Path B  |

The wrapper sets `stewardMode = true` when Path B wins. This flag controls two
downstream behaviors:

1. **Readiness polling is skipped.** In steward mode, the steward controls
   readiness via `POST /readyz/set`, so the autonomous `queryAndUpdateEtcdReadiness`
   goroutine is not started.
2. **Leadership writer runs in both modes** but only the steward uses the
   `/steward/leader` key for leader-only coordination (snapshots, defrag).

### Race Implementation

```
Setup()
  |
  +-- start HTTP server (mux with /readyz, /readyz/set, /stop, /embedded-etcd)
  |
  +-- goroutine A: etcdInitializer.Run(ctx) --> cfgChan
  |
  +-- goroutine B: poll a.embeddedEtcdRequested every 500ms --> cfgChan
  |
  +-- select { case cfg := <-cfgChan: ... case <-ctx.Done(): ... }
```

The HTTP server must start before either goroutine completes because Path B
depends on the `/embedded-etcd` endpoint being reachable. In the legacy flow
this is harmless -- no caller hits the steward-specific endpoints.

## New Endpoints

### POST /embedded-etcd

Accepts a raw YAML etcd configuration in the request body. The handler:

1. Reads the full body via `io.ReadAll`.
2. Writes the body to a temp file at `$TMPDIR/etcd-steward-config.yaml` with
   mode 0600.
3. Parses the file via `embed.ConfigFromFile(tmpFile)`.
4. Sets `cfg.PeerTLSInfo.SkipClientSANVerify = true` to allow joining existing
   clusters where peer certificates have SANs for the original members only.
5. Stores the parsed config and sets `embeddedEtcdRequested = true` and
   `stewardMode = true` under the mutex.
6. Returns HTTP **202 Accepted** with body `embedded etcd start requested`.

Error responses:
- **405 Method Not Allowed** if the HTTP method is not POST.
- **400 Bad Request** if the body cannot be read or the YAML fails
  `embed.ConfigFromFile` validation.
- **500 Internal Server Error** if the temp file cannot be written.

### POST /readyz/set

Allows the steward to override the readiness probe response. The handler reads
the body and accepts exactly two values:

| Body       | Effect                                                    | Response |
|------------|-----------------------------------------------------------|----------|
| `ready`    | Sets `manualReadyOverride = true`; `/readyz` returns 200  | 200 OK   |
| `unready`  | Sets `manualReadyOverride = false`; `/readyz` returns 503 | 200 OK   |
| (other)    | No effect                                                 | 400 Bad Request |

Error responses:
- **405 Method Not Allowed** if the HTTP method is not POST.
- **400 Bad Request** if the body is not `ready` or `unready`.

### Readiness Resolution

The existing `/readyz` handler now evaluates readiness as:

```
ready = etcdReady || manualReadyOverride
```

In legacy mode `etcdReady` is updated by the polling goroutine and
`manualReadyOverride` stays false. In steward mode `etcdReady` stays false
(no polling goroutine) and the steward sets `manualReadyOverride` via
`POST /readyz/set`.

## Leadership Writer

A new goroutine `watchLeadership()` is started unconditionally in `Start()`.
It enables the steward to discover the current etcd leader without maintaining
its own leader election:

1. Polls `etcdClient.Status()` on the local endpoint every **5 seconds**.
2. Compares `resp.Leader` to the previously observed leader ID.
3. On leadership change, if this member is the new leader
   (`resp.Leader == resp.Header.MemberId`), writes the `POD_NAME` environment
   variable to the etcd key `/steward/leader` with a **3-second** operation
   timeout.
4. If this member is not the leader, no write occurs (the leader writes its own
   name).

### Key Details

| Parameter          | Value                |
|--------------------|----------------------|
| Etcd key           | `/steward/leader`    |
| Poll interval      | 5 seconds            |
| Operation timeout  | 3 seconds            |
| Value written      | `$POD_NAME`          |

The steward reads this key to determine which pod should execute leader-only
operations such as full snapshots and defragmentation. The key has no TTL; it is
overwritten on every leadership change.

## Concurrency Model

The `Application` struct guards mutable state with a `sync.Mutex` named `mu`.
Three fields are protected:

| Field                    | Writers                          | Readers                    |
|--------------------------|----------------------------------|----------------------------|
| `embeddedEtcdRequested`  | `startEmbeddedEtcdHandler`       | Path B goroutine, `Setup`  |
| `manualReadyOverride`    | `setReadinessHandler`            | `readinessHandler`         |
| `cfg`                    | `startEmbeddedEtcdHandler`       | Path B goroutine           |

The `stewardMode` field is set once during `Setup()` after the race resolves
and is read without a lock in `Start()` because it is not modified after that
point.

The `etcdReady` field is written only by `queryAndUpdateEtcdReadiness()` (a
single goroutine) and read by `readinessHandler()`. In steward mode neither
goroutine runs, so no concurrent access occurs.

## Application Struct Changes

```go
type Application struct {
    // ... existing fields ...

    // mu guards fields that can be modified by HTTP handlers concurrently.
    mu sync.Mutex

    // embeddedEtcdRequested is set to true when the steward posts a config
    // to /embedded-etcd.
    embeddedEtcdRequested bool

    // manualReadyOverride allows the steward to override the readiness probe
    // via /readyz/set.
    manualReadyOverride bool

    // stewardMode indicates that the wrapper is being driven by etcd-steward
    // (POST /embedded-etcd was received) rather than by the legacy
    // backup-restore sidecar flow.
    stewardMode bool
}
```

## Backward Compatibility

| Aspect                     | Legacy (backup-restore)          | Steward                       |
|----------------------------|----------------------------------|-------------------------------|
| `/readyz` endpoint         | Unchanged (polls etcd)           | Controlled via /readyz/set    |
| `/stop` endpoint           | Unchanged                        | Unchanged                     |
| CLI flags                  | No changes                       | No changes                    |
| Container env vars         | No new vars required             | `POD_NAME` used for leader key|
| Image tag                  | Same image works with both       | Same image works with both    |
| `EtcdInitializer` interface| Unchanged                        | Unused (Path A loses race)    |

The new endpoints (`/embedded-etcd`, `/readyz/set`) are inert when
backup-restore is the sidecar because nothing calls them.

## Test Coverage

Unit tests in `steward_test.go` cover:

- `POST /embedded-etcd` with valid config sets `stewardMode`, `embeddedEtcdRequested`,
  and parses config with `SkipClientSANVerify = true`.
- `POST /embedded-etcd` with invalid YAML returns 400 and does not set steward mode.
- `GET /embedded-etcd` returns 405 Method Not Allowed.
- `POST /readyz/set` with `ready` sets override, `/readyz` returns 200.
- `POST /readyz/set` with `unready` clears override, `/readyz` returns 503.
- `POST /readyz/set` with invalid body returns 400.
- `GET /readyz/set` returns 405 Method Not Allowed.
- `Setup()` with a blocking legacy initializer and a delayed `POST /embedded-etcd`
  correctly resolves via the steward path.

## Version Plan

| Version    | Sidecar support              |
|------------|------------------------------|
| v0.6.x    | etcd-backup-restore only     |
| v0.7.x    | etcd-backup-restore + steward (this spec) |
| v0.8.x    | Remove legacy file-permission migration code (TODO in source) |

## Open Questions

1. **Temp file cleanup.** The handler writes to `$TMPDIR/etcd-steward-config.yaml`
   but does not clean it up after `embed.ConfigFromFile` parses it. This is
   acceptable for container environments where the temp directory is ephemeral,
   but should be considered for long-running development setups.

2. **Leader key TTL.** The `/steward/leader` key currently has no TTL. If a leader
   pod is force-killed without a graceful shutdown, the key will contain a stale
   pod name until the new leader writes its own. The steward must tolerate stale
   values by verifying the pod exists before relying on the key.

3. **HTTP server TLS.** The HTTP server starts early in `Setup()` before the etcd
   config (and therefore TLS settings) are available. In steward mode the server
   starts as plain HTTP. If the steward-to-wrapper communication needs TLS, the
   server would need to be restarted after config is received, or TLS must be
   configured independently of the etcd config.
