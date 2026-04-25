# UseEtcdSteward: Complete Specification & Plan

**Version**: 1.0 | **Date**: 2026-04-25 | **Status**: Draft
**Feature Gate**: `UseEtcdSteward` (Alpha, disabled by default)

---

## Quick Reference

| Question | Answer |
|----------|--------|
| **What** | Replace etcd-backup-restore sidecar with etcd-steward |
| **Why** | Modular architecture, EtcdMember CRD, Go native tests, inverted init flow |
| **Risk** | Data integrity — mitigated by feature gate + toggle test + identical snapshot format |
| **Gate default** | `false` — zero impact on existing production |
| **Toggle tested** | Steward → backup-restore → zero data loss, 40s recovery |
| **Test results** | 29/29 e2e (none provider), unit + integration all pass |
| **PRs** | 6 incremental PRs, each independently safe |
| **Timeline** | Alpha Q2 2026 → Beta Q4 2026 → GA Q2 2027 → Removal Q3 2027 |
| **Repos** | etcd-druid (28 files), etcd-steward (14 files), etcd-wrapper (12 files) |

---

## Document Index

All documents live in the etcd-druid repository. Each is self-contained — you can read
any document independently. Together they form the complete spec.

### Tier 1: Why & What (read first)

| Doc | Path | Lines | What You'll Learn |
|-----|------|-------|-------------------|
| **Proposal** | [docs/design/proposal.md](design/proposal.md) | 60 | Motivation, scope, capabilities, impact summary |
| **Why etcd-steward** | [docs/design/01-why-etcd-steward.md](design/01-why-etcd-steward.md) | 581 | 5 problems with backup-restore, 4 design decisions with tradeoffs, current vs new architecture diagrams, compatibility contract |

### Tier 2: Specs (one per repo)

| Doc | Path | Lines | What You'll Learn |
|-----|------|-------|-------------------|
| **etcd-druid spec** | [docs/design/02-druid-changes-spec.md](design/02-druid-changes-spec.md) | 327 | Every druid change organized by component: feature gate, CRD, builder, status reconciler, compaction, peer TLS, RBAC, snapshot leases. Each with code snippets and WHY |
| **etcd-steward spec** | [docs/design/03-steward-spec.md](design/03-steward-spec.md) | 369 | 20 packages, 11-phase startup, runtime components, DEP-04 state machine, key design patterns (nil interface gotcha, panic recovery, TLS from flags) |
| **etcd-wrapper spec** | [docs/design/04-wrapper-spec.md](design/04-wrapper-spec.md) | 282 | Dual-mode race (legacy vs steward), POST /embedded-etcd, POST /readyz/set, leadership writer, backward compatibility |

### Tier 3: How to Ship

| Doc | Path | Lines | What You'll Learn |
|-----|------|-------|-------------------|
| **PR plan** | [docs/design/05-pr-plan.md](design/05-pr-plan.md) | 255 | 6 incremental PRs with exact file lists, line counts, risk levels, dependency graph, test matrix per PR |
| **E2E testing** | [docs/design/06-e2e-testing-spec.md](design/06-e2e-testing-spec.md) | 572 | Prerequisites, image build steps, all test suites, results tables, 10 issues found & fixed, reproduction commands |
| **Rollout plan** | [docs/proposals/08-etcd-steward-rollout-plan.md](proposals/08-etcd-steward-rollout-plan.md) | 770 | 4-phase production rollout (Alpha→Beta→GA→Removal), cross-repo release order, CI/CD integration, risk mitigation, success metrics, toggle test appendix |

### Tier 4: Operational Guides

| Doc | Path | Lines | What You'll Learn |
|-----|------|-------|-------------------|
| **Migration guide** | [docs/deployment/use-etcd-steward.md](deployment/use-etcd-steward.md) | 201 | How to enable/disable, verification steps, rollback procedure, known limitations |
| **Feature gates** | [docs/deployment/feature-gates.md](deployment/feature-gates.md) | 80 | All feature gates including UseEtcdSteward |
| **EtcdMember impl** | [docs/concepts/etcdmember-implementation.md](concepts/etcdmember-implementation.md) | 231 | DEP-04 state machine, transition table, how steward reports, how druid reads |
| **Architecture** | [docs/concepts/use-etcd-steward-architecture.md](concepts/use-etcd-steward-architecture.md) | 263 | ASCII diagrams: sidecar selection, pod architecture, data flow, toggle mechanism |

---

## Reading Order

### For maintainers reviewing the PR:
1. `proposal.md` (2 min) — scope and impact
2. `02-druid-changes-spec.md` (15 min) — every change with WHY
3. `05-pr-plan.md` (5 min) — PR structure and test matrix

### For developers implementing from scratch:
1. `01-why-etcd-steward.md` (20 min) — architecture and decisions
2. `03-steward-spec.md` (15 min) — steward internals
3. `04-wrapper-spec.md` (10 min) — wrapper changes
4. `06-e2e-testing-spec.md` (15 min) — how to test

### For SREs rolling out to production:
1. `deployment/use-etcd-steward.md` (5 min) — enable/disable/rollback
2. `08-etcd-steward-rollout-plan.md` (15 min) — phased rollout
3. `concepts/etcdmember-implementation.md` (10 min) — what EtcdMember tracks

### For AI agents recreating the implementation:
Read all Tier 1 + Tier 2 docs in order. The specs use RFC 2119 language (SHALL/MUST)
and WHEN/THEN scenarios that map directly to implementation and tests.

---

## Repositories & Changes Summary

### gardener/etcd-steward (new sidecar — release v0.1.0 first)
```
cmd/etcdsteward/main.go          — 11-phase daemon, runtime component wiring
internal/config/                  — configuration model + flags
internal/statemachine/            — DEP-04 state machine (New→Initializing→Starting→Started)
internal/member/                  — EtcdMember status updater + K8s recorder
internal/lease/                   — K8s lease renewal with HolderIdentityFunc + annotations
internal/snapshotter/             — full + delta snapshot taking
internal/restorer/                — restore from backup (with panic recovery)
internal/server/                  — HTTP server (/healthz, /snapshot/*, /initialization/*)
+ 12 more packages (alarm, bootstrapper, compactor, compression, defrag, etcdclient, gc, leaderwatch, metrics, snapstore, validator, errors)
```

### gardener/etcd-wrapper (compat changes — release v0.7.1 second)
```
internal/app/app.go              — dual-mode Setup() racing legacy vs steward paths
internal/app/readycheck.go       — POST /embedded-etcd, POST /readyz/set endpoints
internal/app/leadership.go       — writes leader pod name to /steward/leader key
```

### gardener/etcd-druid (operator — PRs 1-6 third)
```
API:         feature gate, EtcdMember CRD, constants, image vector
Builder:     steward container args, TLS volumes, readiness probe
Controllers: status reconciler, compaction, EtcdOpsTask HTTP
RBAC:        lease create, etcdmembers permissions
Config:      Helm values, ClusterRole
Tests:       e2e infrastructure, pod filtering, snapshot verification
```

---

## Feature Gate State Machine

```
 UseEtcdSteward=false (default)     UseEtcdSteward=true
┌─────────────────────────┐     ┌──────────────────────────┐
│ etcd-backup-restore      │     │ etcd-steward              │
│ ├─ Lease-based status    │     │ ├─ EtcdMember CRD status  │
│ ├─ GET /snapshot/full    │ ──► │ ├─ POST /snapshot/full    │
│ ├─ HTTPS + TLS           │     │ ├─ HTTP (no sidecar TLS)  │
│ ├─ Snapshot leases       │     │ ├─ EtcdMember.Snapshots   │
│ └─ Wrapper polls sidecar │     │ └─ Steward POSTs wrapper  │
└─────────────────────────┘     └──────────────────────────┘
         ▲                               │
         │    Toggle: disable gate       │
         │    redeploy druid             │
         │    40s recovery               │
         └───────────────────────────────┘
```

---

## Verified Results

| Test | Gate=false | Gate=true | Toggle |
|------|-----------|-----------|--------|
| TestBasic (6 subtests) | 6/6 PASS | 6/6 PASS | PASS |
| TestRecovery (7) | — | 7/7 PASS | — |
| TestScaleOut (5) | — | 5/5 PASS | — |
| TestClusterUpdate (4) | — | 4/4 PASS | — |
| TestTLSAndLabelUpdates (6) | — | 6/6 PASS | — |
| TestSecretFinalizers (1) | — | 1/1 PASS | — |
| Unit tests | ALL PASS | ALL PASS | — |
| Integration tests | ALL PASS | ALL PASS | — |

---

## Known Gaps (follow-up work)

| Priority | Gap | Impact | Tracked |
|----------|-----|--------|---------|
| P0 | Compact subcommand fails as K8s Job | Compaction tests fail with local provider | Follow-up PR |
| P1 | Steward HTTP server doesn't support TLS | Sidecar-to-sidecar TLS not possible | Beta blocker |
| P2 | Snapshot lease HolderIdentity not set to revision | Legacy monitoring tools can't read revisions | Beta |
| P2 | Defrag status not in EtcdMember | No defrag tracking | Beta |
| P3 | BackupReady condition not wired | Condition always skipped in tests | GA |

---

*Total documentation: 12 documents, ~4,000 lines*
*Generated: 2026-04-25*
