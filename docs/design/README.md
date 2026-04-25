# OpenSpec: UseEtcdSteward

## Overview

This is the OpenSpec for the `UseEtcdSteward` feature — replacing etcd-backup-restore
with etcd-steward as the sidecar for etcd clusters managed by etcd-druid.

Following the [OpenSpec](https://github.com/Fission-AI/OpenSpec) spec-driven development
approach: specifications are written BEFORE (or alongside) implementation, using
structured artifacts that AI coding assistants and human reviewers can follow reliably.

## Artifact Map

```
openspec/use-etcd-steward/
├── proposal.md          ← WHY: motivation, scope, impact
├── specs/
│   ├── 01-architecture/ ← WHAT: system behavior specs
│   │   └── spec.md      → docs/design/01-why-etcd-steward.md
│   ├── 02-druid/
│   │   └── spec.md      → docs/design/02-druid-changes-spec.md
│   ├── 03-steward/
│   │   └── spec.md      → docs/design/03-steward-spec.md
│   ├── 04-wrapper/
│   │   └── spec.md      → docs/design/04-wrapper-spec.md
│   └── 05-e2e/
│       └── spec.md      → docs/design/06-e2e-testing-spec.md
├── design.md            ← HOW: technical decisions
└── tasks.md             ← DO: PR plan → docs/design/05-pr-plan.md
```

## Documents

| # | Document | Lines | Purpose | RFC 2119 |
|---|----------|-------|---------|----------|
| [01](01-why-etcd-steward.md) | Why etcd-steward | 581 | Problem, decisions, architecture | Motivation |
| [02](02-druid-changes-spec.md) | etcd-druid changes | 327 | Every druid change with WHY | SHALL/MUST |
| [03](03-steward-spec.md) | etcd-steward internals | 369 | 17 packages, 11-phase startup | SHALL |
| [04](04-wrapper-spec.md) | etcd-wrapper compat | ~200 | Dual-mode race, new endpoints | SHALL |
| [05](05-pr-plan.md) | PR plan | 255 | 6 PRs, file lists, test matrix | Tasks |
| [06](06-e2e-testing-spec.md) | E2E testing | 572 | Results, issues, reproduction | Verified |

## Status

- **Phase**: Implementation complete, spec documentation phase
- **Feature Gate**: `UseEtcdSteward` (Alpha, disabled by default)
- **Test Results**: 29/29 e2e pass (none), 53/58 (local), gate toggle verified
- **Repos**: etcd-druid (28 files), etcd-steward (14 files), etcd-wrapper (12 files)

## Capabilities

### New Capabilities
- `etcdmember-lifecycle`: EtcdMember CRD tracks per-member state (DEP-04)
- `steward-sidecar`: etcd-steward replaces backup-restore when gate enabled
- `feature-gate-toggle`: Live switch between steward and backup-restore

### Modified Capabilities
- `status-reconciliation`: Reads from EtcdMember CRDs instead of leases
- `compaction-trigger`: Reads snapshot revisions from EtcdMember.Status.Snapshots
- `snapshot-api`: Uses POST /snapshot/full (HTTP) instead of GET (HTTPS)

### Unchanged Capabilities (gate=false)
- ALL existing etcd-backup-restore behavior — zero changes when gate disabled

## Impact

### Affected Repos
- `gardener/etcd-druid` — 28 files, +979/-55 lines
- `gardener/etcd-steward` — 14 files (new sidecar)
- `gardener/etcd-wrapper` — 12 files (steward-compat)

### Affected Components
- StatefulSet builder (container spec generation)
- Etcd status reconciler (member status source)
- Compaction controller (snapshot revision source)
- EtcdOpsTask controller (HTTP client)
- Snapshot lease component (skipped when steward)
- Role component (RBAC additions)

### Not Affected (gate=false)
- All existing controller logic
- All existing e2e tests
- All existing Helm chart behavior
- etcd-backup-restore sidecar
