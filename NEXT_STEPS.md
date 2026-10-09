# Next steps

The assignment stops here. This file records what I would do next and why, in rough priority order. Everything in the README's [Verification](README.md#verification) table works today. The items below are about making the system faster, safer to change, and ready for production.

**Where things stand**

- **Done:** the full system as described in the README. A second round of UI work then simplified the Orders page: one filter row of multi-select dropdowns (Status with live counts, Source, Type, Flags), a Reset button, a rows-per-page selector, and a large **Needs review** alert card with a one-click filter. It replaced the stat tiles and status tabs.
- **Planned but not done:** the backend optimization pass and the automated test suite. Both were scoped in detail, and a backend prototype was started. The work was stopped to keep the assignment within its time budget. The prototype isn't merged, because it was never re-measured or re-verified end to end.

---

## 1. Backend performance

### Measured baseline

These figures are from the code as delivered: compose stack, 1 api + 1 worker with 4 inbox goroutines, on a 2-vCPU sandbox.

| Measurement | Result |
|---|---|
| Webhook ingest, 10,000 new orders, 32 concurrent clients | **1,286 req/s**, p50 20 ms, p99 91 ms |
| Inbox drained, all 10,000 dispatched | 90 s (**about 110 orders/s** through the workers) |
| CSV import, 20,000 rows (2.4 MB) | HTTP response in 6.6 s, then **171 s** for workers to finish (about 117 rows/s) |
| Idle load, 60 orders, no traffic | **105 queries/s** against Postgres, 9 connections |
| Idle load, 30,000 orders (20,000 scheduled) | DB CPU about **38%**; the scheduler evaluated `app_now()` once per scheduled row, which is 320,000 calls per 30 s |
| `GET /api/stats` (polled every 5 s by every open dashboard) | median **100 ms** at 30,000 orders |
| `GET /api/orders?q=…` (search) | median 126–140 ms (sequential scan) |
| `GET /api/orders?sort=customer&page=300` | median 152 ms |
| `GET /api/orders/export.csv` (all orders) | median 807 ms |
| Order detail / related / dead letters | 2–7 ms (fine) |

Ingest at the edge is already far beyond the 100k/day target (about 1.2 req/s on average). The weak spots are **worker throughput**, **idle database load**, and **dashboard queries that grow with table size**.

### Planned changes

These were designed, and most were prototyped. Each needs to be measured before and after, and the README verification table re-run.

1. **Faster read queries** (new migration `002`; never edit `001`, since existing databases already have it applied):
   - store `item_count` on `orders` instead of counting items per row in every list query;
   - indexes that match the list's sort orders, and partial indexes for the inbox and outbox claim queries;
   - a `pg_trgm` index so search stops scanning the whole table;
   - a cheaper `/api/stats`: one grouped pass instead of 11 subqueries, plus counters for the inbox and outbox backlogs;
   - the scheduler computes the release time once per tick, not once per row.
2. **Batch inbox claiming:** claim up to N rows per transaction with `SKIP LOCKED`, and process each one inside a savepoint, so one bad payload doesn't roll back the rest. Exactly-once effects stay the same.
3. **Instant wake-ups:** Postgres `LISTEN/NOTIFY` wakes workers, the dispatcher and the outbox sender when there is work, with a slow fallback poll (about 2 s) instead of polling every 100–300 ms. This cuts most of the idle query load and lowers latency. Two details need care:
   - wake everything after a reconnect, because notifications can be lost while disconnected;
   - batch the edge's notifications, because a commit that issues NOTIFY takes a cluster-wide lock.
4. **Bulk inserts:** one multi-row `INSERT … SELECT FROM unnest(...)` for CSV uploads, webhook arrays/NDJSON and poller batches, with duplicate detection identical to inserting one at a time.
5. **Fewer statements per order:** write all of an order's items, and its history events, in one statement each, and insert new orders straight into their first resting state (`queued` or `scheduled`). A webhook order would drop from about 5 statements plus one per item to 3.
6. **Concurrent robot delivery:** the outbox sender claims up to 20 messages and posts them concurrently over keep-alive connections.
7. **Resource limits and shutdown:**
   - size each process's connection pool from its roles (it currently opens `10 + 2 × WORKER_CONCURRENCY` whatever it runs);
   - CPU and memory limits plus `GOMEMLIMIT` per service in `docker-compose.yml`;
   - a graceful shutdown that lets in-flight batches finish within a 15 s grace period.

**How to verify:** re-run the baseline above, then the README checks on a fresh database:

- 998 / 267 / 60 orders, and all 264 API item statuses match the file;
- time warp releases scheduled orders;
- scaled to 3 api / 4 workers with a burst: no double processing or dispatch;
- killing the leader fails over;
- restarting Postgres recovers (including the LISTEN connections).

Also apply `002` on a database populated under `001` to prove the upgrade path.

---

## 2. Automated tests

**Today:** unit tests cover `internal/normalize` (79.8%), which holds the CSV splitting, meal slots, hashing and status rules. That's **8.3% of the Go code overall**. Everything else (workers, poller, scheduler, dispatcher, leader election, HTTP API, frontend) was checked by hand, as recorded in the README.

**Planned, in order of value:**

1. **Go integration tests against a real Postgres**, started and thrown away by the test run (`docker run` or testcontainers, with a `DATABASE_URL` override for CI). Turn every hand-run README check into a repeatable test:
   - **Webhook sample file:** 998 orders; duplicates recorded, never applied; cancel before and after dispatch; ID conflict quarantined (first order wins); empty orders rejected and dead-lettered.
   - **CSV:** cumulative uploads 1→4 give 267 orders, re-uploads add nothing, and an upload is all-or-nothing.
   - **Poller against an `httptest` mock:** 200 advances the cursor; 500 with partial data applies the data but holds the cursor; network errors back off; deltas arriving out of order never move an item backwards.
   - **Scheduler with a time-warped clock:** releases exactly the due orders.
   - **Dispatcher and outbox:** one robot message per order; retries on 503; flagged after the maximum number of attempts.
   - **Concurrency:** N workers on one inbox never double-apply; two leader candidates give one active; the leader's connection dying gives failover.
   - **HTTP handlers:** filters (multi-value status/source/kind, the OR semantics of `flags`, search, sort, pagination), export columns, related deliveries, time warp, and the 4xx paths.
2. **More Go unit tests:** config parsing, mock API replay (500-then-success, `has_more`), robot payload building, and a `make coverage` target with a CI threshold.
3. **Frontend unit tests (Vitest + Vue Test Utils):** `filters.ts` (parse/serialize, selection summaries, page-size resolution), `MultiSelect` (keyboard, ARIA, outside click, clear), the URL ↔ filter sync and Reset behaviour on the Orders page, the review alert toggle, and the formatting helpers.
4. **Browser end-to-end smoke test (Playwright)** against the compose stack: load sample data, filter, open an order, cancel it, time-warp, check the review queue. Run it in CI after `docker compose up`.
5. **Soak test:** about 10× the target rate for an hour, watching inbox age, memory and connection count.

---

## 3. Frontend polish

- **Filter row:** with all four filters active it wraps, and the Live toggle drops to a second line. Give the row a fixed layout, or show the active filters as removable chips.
- **Dropdowns:** clicking a table row while a dropdown is open closes the dropdown and also opens that order. The outside click should only close it.
- **Mobile:** the top navigation cuts off the Review queue badge.
- **Polling polish:** pause it while the tab is hidden for a long time. Consider server-sent events for the counts once the backend has LISTEN/NOTIFY.
- **Order detail:** collapse the raw payloads by default and add a compact mode for everyday use.

---

## 4. Production readiness

1. **Auth and tenancy:**
   - sign webhook requests (an HMAC key per platform);
   - authenticate the dashboard (OIDC), with role-based access for admin actions;
   - remove the demo endpoints (time warp, reset) from production builds.
2. **Observability:**
   - Prometheus metrics for ingest rate, inbox age and depth, dead-letter rate, dispatch latency and poller lag, with alerts;
   - OpenTelemetry traces from the edge to the robot ack;
   - a request id carried through events.
3. **Review workflow:** let operators act on review items (merge or replace a conflicting order, apply an item-change request, re-drive a dead letter after a fix). Today they can only inspect and resolve them.
4. **Menu and pricing service:** the menu catalog is derived from the sample data. A real one would price CSV orders, validate items and map them to robot recipes.
5. **Robot integration:** a real contract (recipes and stations), status callbacks (`assembling → ready`), and capacity-aware dispatch that throttles releases to robot throughput.
6. **Scaling past one Postgres:** partition `order_events` and `inbox` by time and archive old rows; read replicas for the dashboard. Put Kafka/SQS in front of the inbox only if ingest grows by orders of magnitude, keeping the same idempotent handlers.
7. **Deployment:** Helm chart, autoscaling on API CPU and inbox depth, PodDisruptionBudgets, managed Postgres with point-in-time recovery.
8. **Data retention and privacy:** customer names are personal data. Define how long payloads are kept in `inbox`, `dead_letter` and event history.
