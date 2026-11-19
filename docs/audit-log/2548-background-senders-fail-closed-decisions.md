# 2548 — Background senders fail closed on a flag-store error; the catch-up sweep: decisions log

Issue: mgoodric/security-atlas#2548. Follow-ups from the review of #2535
(#2175). See `docs/audit-log/2175-background-module-switches-decisions.md`
for the gating this builds on.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The fallback to the seed default
  was raised in the review of #2535, not by a test.
- **detection_tier_target:** integration. The decision and personnel
  notifier integration tests now sweep once with a flag store that fails,
  and were observed failing against the lenient read before the fix.

## Decisions

### D1 — a strict read for background senders; request paths unchanged

`featureflag.Enabled` falls back to a switch's seed default when the flag
store's read fails, so a request keeps working through a DB blip. With no
request behind it, that fallback can send a reminder for a module the
tenant switched off. #2548 adds a strict read for that case:

| Read                        | On a flag-store error                | Used by                                                            |
| --------------------------- | ------------------------------------ | ------------------------------------------------------------------ |
| `featureflag.Enabled`       | Seed default, nil error              | Request paths (unchanged)                                          |
| `featureflag.EnabledStrict` | Error wrapping `ErrStoreUnavailable` | `Views.SwitchedOnStrict`                                           |
| `Views.SwitchedOn`          | Seed default (follows `Enabled`)     | MCP proposal confirm (a request; unchanged)                        |
| `Views.SwitchedOnStrict`    | Error                                | Board pack generator; `Views.Reaches`                              |
| `Views.Reaches`             | Error (switch read strictly)         | Decision and personnel notifiers; email, Slack and webhook digests |

`Reaches` reads strictly outright rather than gaining a second strict
variant: every caller of it is a background sender, so there is no request
path to keep lenient.

What each sender does with the error:

- **Decision-overdue and personnel overdue-offboarding notifiers:** an error
  wrapping `featureflag.ErrStoreUnavailable` aborts that tenant's sweep. The
  sweep loop already logs a failed tenant and moves to the next. Nothing
  writes a dedup marker for the skipped items, so the next sweep retries
  them. Any other error from `Reaches` (an access read) still skips just
  that recipient, as #2175 D4 set.
- **Digests:** `notify.HiddenKinds` already returns the error, and the
  channel returns it before it claims the day's digest key. Nothing is sent,
  and the next scheduler sweep tries again.
- **Board pack generator:** `Generate` returns the error, so no pack is
  built from a guessed set of sections. Generation is operator-triggered; a
  retry after the store recovers produces the pack.

### D2 — the catch-up sweep after a module is switched back on is not capped

The ticket asks to cap or batch the first sweep after a switch-on, or to
decide and document. Decision: **no cap.** What a switch-on releases today:

| Sender                       | What goes out on the first sweep after switch-on                                   |
| ---------------------------- | ---------------------------------------------------------------------------------- |
| Decision-overdue notifier    | One in-app row per still-overdue decision, to its decision maker                   |
| Personnel overdue notifier   | One in-app row per still-overdue checklist, to each active user with access        |
| Email, Slack, webhook digest | One digest per recipient per period, with counts; the burst becomes larger numbers |

Why no cap:

- **Every row names a real, still-overdue item.** A cap delays some of them
  to later sweeps for no gain, and a "went overdue recently" window would
  silence the longest-overdue items forever. Those are the ones most worth
  seeing.
- **The noisy channels already batch.** The external channels carry one
  digest, not N messages. The N rows land in the in-app notification list,
  which a person reads at their own pace.
- **The burst is bounded by real work.** It is at most the number of
  overdue items in the module, the same count the module's own pages show
  when it is switched back on.
- **A cap needs state the system does not have.** Knowing "first sweep after
  a switch-on" means reading the flag audit log per tenant per sweep, a new
  mechanism for a cosmetic gain.

Revisit if a tenant reports the switch-on burst as a problem. The fix then
is a per-recipient roll-up row ("N decisions are overdue"), not a cap that
hides items.
