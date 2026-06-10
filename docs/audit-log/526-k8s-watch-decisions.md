# 526 — K8s watch-based event-driven (subscribe) profile · decisions log

**Slice type:** JUDGMENT (profile shape + watch-loop design + dedup/coalescing

- stable-field choices)

* detection_tier_actual: none
* detection_tier_target: none

No bug surfaced during the slice. The watch lifecycle's tricky branches
(reconnect, 410-Gone re-list, burst coalesce) were covered by the faked-stream
unit tests as written, not discovered as defects.

---

## Decisions made

### D1 — watch (read-only API) vs the Kubernetes audit log · **confidence: high**

**Options:** (a) consume the Kubernetes **audit log**; (b) consume a **`watch`**
against the same read-only API surfaces the pull path reads.

**Chosen: (b) watch.** The audit log names the actor of each change directly
(stronger repudiation signal), but it requires control-plane audit-policy access
that managed clusters (EKS/GKE/AKS default tiers, most hosted control planes)
frequently do not expose to a workload ServiceAccount. The `watch` against the
read-only API is **portable to every cluster** and reaches **exactly** the
surfaces the pull path already reads — so it adds at most a `watch` verb, no new
resource. The audit-log path is documented as a **future / fallback** option
(README "Profiles", this log) but not implemented. The slice spec's RECOMMENDED
call.

### D2 — watch-loop shape: hand-rolled vs `cache.Reflector` · **confidence: high**

**Options:** (a) pull in `k8s.io/client-go` and use a `cache.Reflector`; (b)
hand-roll the reflector loop against a narrow fakeable seam.

**Chosen: (b) hand-rolled.** The connector deliberately avoids `client-go`
(`rbac/client.go`, `workload/client.go`, `k8slist`: thin-HTTP, "to keep the
dependency tree small"). Pulling client-go in for one watch loop would invert
that decision and bloat the binary. The loop is a small, well-understood state
machine (LIST → watch-from-RV → bookmark-advance → re-watch-on-close →
re-LIST-on-410); hand-rolling it against the `watch.Source` / `watch.Stream`
seam keeps the dependency story consistent **and** makes the path fully fakeable
(no live cluster, no `envtest`) — the non-negotiable test requirement. The
`watch` package (`Run` + the reflector loop) is the new surface; the HTTP
`Source`/`Stream` is the thin concrete adapter, mirroring `k8slist.Reader`.

### D3 — `allowWatchBookmarks` + the resume-point model · **confidence: high**

The watch is opened with `allowWatchBookmarks=true`. Bookmark events carry only
a fresh `resourceVersion`; the loop advances its resume point on them **without
emitting a record and without a re-LIST**. This is the standard reflector
efficiency: on a routine server-initiated close, the connector re-watches from
the last (often bookmark-advanced) RV rather than re-listing the whole resource.
A re-LIST happens **only** on a 410 Gone (D4) or a `List`/`Watch`-open error
backoff.

### D4 — 410-Gone handling: re-list **and emit** · **confidence: medium**

On a 410 Gone (`resourceVersion too old` — the server compacted past our RV) the
RV is unusable. The loop drops it and re-LISTs for a fresh RV, then resumes the
watch. **The re-LIST emits a record per current object.** Rationale: the re-list
IS fresh state, and the slice-487 hour-window idempotency key makes re-emission
**safe** — a re-list record for binding X in hour H collapses with any
pull-emitted or prior-watch-emitted record for X in H onto one ledger row. So
emitting on re-list costs nothing (deduped) and guarantees the current state is
captured even if the watch missed an event during the gap. The alternative
(re-list silently, emit nothing, rely on the pull backstop) was rejected because
it would leave a freshness gap until the next scheduled pull. **Revisit** if the
hour-window proves too coarse for a high-velocity cluster (see revisit list).

### D5 — RBAC watch streams `rolebindings` only; rules resolve empty · **confidence: medium**

The pull path resolves each binding's **role rules** by cross-referencing
separate role/clusterrole lists (`rbac.Client.ListBindings`). A single watch
event on a `rolebinding` does **not** carry the referenced role's rules. Rather
than issue a per-event role lookup (N extra reads per event — a DoS amplifier
under churn), the watch-emitted `RawBinding` carries the **binding identity**
(name/scope/namespace/roleRef/subjects) with **empty `Rules`** — so
`grants_wildcard` is `false` on the watch record. The **pull profile** (the
reconciliation backstop, recommended every 24h) carries the full rule picture;
the hour-window key collapses the watch and pull records, and the pull record's
resolved rules win at the ledger when both land in the same hour (last-writer on
the idempotent key). The watch surface is `rolebindings` (the most-churned RBAC
object); cluster-scoped `clusterrolebindings` and the workload daemonset/
statefulset kinds are covered by the bootstrap LIST + pull (a documented
follow-on could add their watch streams). **Revisit** if real auditors need
rule-level reach in near-real-time (then resolve rules per-event with a bounded
cache, or watch roles/clusterroles too).

### D6 — coalescing/queue bound · **confidence: high**

Two layers. **Primary (durable):** the slice-487 hour-window idempotency key —
every event for resource R in hour H hashes to one key, so a burst of edits to R
within H collapses to one ledger row at the platform (threat-model D). **Secondary
(in-process pre-filter):** a bounded per-hour key set (`coalescer`, default cap
100 000, hour-rollover reset) lets the loop recognise an already-seen key this
hour and log the coalesce; when the cap is exceeded the set is shed (the platform
key still dedups, so correctness holds — only the in-process filter resets). There
is **no unbounded work queue**: the loop processes one event at a time
synchronously per surface; back-pressure is the watch stream itself. The cap is
the explicit memory bound the spec asks for.

### D7 — DELETED events still emit · **confidence: medium**

A DELETED event builds and pushes the same record (reflecting the last-observed
config at the deletion instant) rather than being dropped. The evidence ledger is
append-only (invariant #2); a deletion is itself a fact worth recording, and the
record carries the same idempotency key so it does not double-write. The emitter
receives the event type and **could** choose to no-op a delete — that is left as
an emitter-level future choice, not baked into the loop.

### D8 — graceful shutdown via signal context · **confidence: high**

`main` wires `signal.NotifyContext(SIGINT, SIGTERM)` and `ExecuteContext`; the
two watch surfaces run under an `errgroup` bound to that context. A signal
cancels the context, both loops return `ctx.Err()`, and `doSubscribe` treats
`context.Canceled` as a clean exit (not a failure). The pre-existing `run`/
`register`/`permissions` subcommands are unaffected by the context plumbing.

---

## Revisit once in use

- **D4 / D6 hour-window granularity.** If a high-velocity cluster changes one
  binding many times across hour boundaries, each hour produces one record — which
  is the intended coalescing — but a maintainer should confirm the 1h bucket is the
  right freshness/volume trade-off against real auditor expectations once the
  product runs against a live cluster. (Lowering the bucket is a connector-wide
  `idem` change, not a watch-local one.)
- **D5 empty rules on the watch RBAC record.** Re-check once auditors actually
  consume near-real-time RBAC evidence: if rule-level reach (the `grants_wildcard`
  signal) is needed faster than the next pull, resolve rules per-event with a
  bounded role cache, or add a `roles`/`clusterroles` watch.
- **D5 watch surface coverage.** The watch streams `rolebindings` + `deployments`.
  If `clusterrolebindings` / `daemonsets` / `statefulsets` churn matters in
  practice, add their watch streams (the loop is already generic over the
  `Source`).
- **D1 audit-log path.** If a deployment has control-plane audit access and wants
  per-change actor attribution, the audit-log consumer is the documented next
  option.
- **D7 delete semantics.** Confirm with the evaluator team whether a DELETED
  binding should emit a "deleted" marker vs the last-observed config; the loop
  passes the event type to the emitter so this is a one-line emitter change.

---

## Confidence summary

| Decision                          | Confidence |
| --------------------------------- | ---------- |
| D1 watch vs audit log             | high       |
| D2 hand-rolled loop               | high       |
| D3 bookmarks / resume             | high       |
| D4 410-Gone re-list-and-emit      | medium     |
| D5 rolebindings-only, empty rules | medium     |
| D6 coalescing bound               | high       |
| D7 DELETED emits                  | medium     |
| D8 graceful shutdown              | high       |

The `medium`-confidence decisions (D4, D5, D7) are the top of the revisit list —
all three are freshness/granularity trade-offs that the hour-window idempotency
key makes **safe** today (no correctness risk), and that a maintainer can tune
once the product runs against a real cluster and real auditors.
