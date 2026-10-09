-- Order ingestion & dispatch schema.
-- Every table is shared by all api/worker replicas; coordination happens via
-- row locks (FOR UPDATE SKIP LOCKED), unique keys and advisory locks.

-- Small key/value table. Holds the demo "time warp" clock offset.
CREATE TABLE app_settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);
INSERT INTO app_settings (key, value) VALUES ('clock_offset', '0 seconds');

-- app_now() is the system's notion of "now": wall clock + time-warp offset.
-- All scheduling decisions go through it, so a demo can jump ahead in time.
CREATE FUNCTION app_now() RETURNS timestamptz LANGUAGE sql STABLE AS $$
    SELECT now() + COALESCE((SELECT value::interval FROM app_settings WHERE key = 'clock_offset'), interval '0');
$$;

CREATE TABLE orders (
    id              bigserial PRIMARY KEY,
    source          text NOT NULL CHECK (source IN ('webhook', 'api', 'csv')),
    external_id     text NOT NULL,
    channel         text NOT NULL DEFAULT '',      -- Overeats / DoorDrop / Grubstub / poll-api / survey
    restaurant      text NOT NULL DEFAULT '',
    customer_first  text NOT NULL DEFAULT '',
    customer_last   text NOT NULL DEFAULT '',
    total           numeric(10, 2),
    notes           text NOT NULL DEFAULT '',
    status          text NOT NULL,
    external_status text NOT NULL DEFAULT '',      -- raw upstream status (API source)
    kind            text NOT NULL CHECK (kind IN ('realtime', 'scheduled')),
    meal            text NOT NULL DEFAULT '',
    scheduled_for   timestamptz,
    flags           text[] NOT NULL DEFAULT '{}',
    needs_review    boolean NOT NULL DEFAULT false,
    dispatched_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX orders_status_idx ON orders (status, id DESC);
CREATE INDEX orders_release_idx ON orders (scheduled_for) WHERE status = 'scheduled';
CREATE INDEX orders_created_idx ON orders (created_at DESC);

CREATE TABLE order_items (
    id              bigserial PRIMARY KEY,
    order_id        bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    item_key        text NOT NULL,                  -- upstream item hash, or position for webhook/csv
    name            text NOT NULL,
    category        text NOT NULL DEFAULT '',
    price           numeric(10, 2),
    qty             int NOT NULL DEFAULT 1,
    external_status text NOT NULL DEFAULT '',
    UNIQUE (order_id, item_key)
);

-- Append-only history: one row per thing that happened to an order.
CREATE TABLE order_events (
    id          bigserial PRIMARY KEY,
    order_id    bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    type        text NOT NULL,
    from_status text NOT NULL DEFAULT '',
    to_status   text NOT NULL DEFAULT '',
    message     text NOT NULL DEFAULT '',
    payload     jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_events_order_idx ON order_events (order_id, id);

CREATE TABLE csv_uploads (
    id          bigserial PRIMARY KEY,
    filename    text NOT NULL,
    sha256      text NOT NULL,
    row_count   int NOT NULL,
    new_count   int NOT NULL,
    dup_count   int NOT NULL,
    rejected    jsonb NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Durable buffer between the ingest edge and the workers. The edge only ever
-- inserts here, so bursts are absorbed by Postgres rather than dropped.
CREATE TABLE inbox (
    id              bigserial PRIMARY KEY,
    source          text NOT NULL,
    external_id     text NOT NULL,
    idem_key        text NOT NULL,                  -- source:external_id:payload_hash
    is_duplicate    boolean NOT NULL DEFAULT false, -- same idem_key seen before
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'dead')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '',
    upload_id       bigint REFERENCES csv_uploads (id),
    received_at     timestamptz NOT NULL DEFAULT now(),
    processed_at    timestamptz
);
-- The first delivery of an idempotency key is unique; repeats are recorded with is_duplicate.
CREATE UNIQUE INDEX inbox_idem_first_idx ON inbox (idem_key) WHERE NOT is_duplicate;
CREATE INDEX inbox_claim_idx ON inbox (next_attempt_at, id) WHERE status = 'pending';
CREATE INDEX inbox_external_idx ON inbox (source, external_id);
CREATE INDEX inbox_processed_idx ON inbox (processed_at) WHERE processed_at IS NOT NULL;

-- Payloads we refused or could not process, kept for human review.
CREATE TABLE dead_letter (
    id          bigserial PRIMARY KEY,
    source      text NOT NULL,
    external_id text NOT NULL DEFAULT '',
    reason      text NOT NULL,
    detail      text NOT NULL DEFAULT '',
    payload     jsonb,
    inbox_id    bigint REFERENCES inbox (id),
    order_id    bigint REFERENCES orders (id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);
CREATE INDEX dead_letter_external_idx ON dead_letter (source, external_id);
CREATE UNIQUE INDEX dead_letter_inbox_idx ON dead_letter (inbox_id) WHERE inbox_id IS NOT NULL;

-- Transactional outbox for messages to the robot. Written in the same
-- transaction as the status change; a sender delivers it with retries.
CREATE TABLE dispatch_outbox (
    id              bigserial PRIMARY KEY,
    order_id        bigint NOT NULL REFERENCES orders (id),
    kind            text NOT NULL CHECK (kind IN ('dispatch', 'cancel')),
    idem_key        text NOT NULL UNIQUE,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz
);
CREATE INDEX dispatch_outbox_claim_idx ON dispatch_outbox (next_attempt_at, id) WHERE status = 'pending';

-- Poller state (one row per upstream API).
CREATE TABLE ingest_cursors (
    name            text PRIMARY KEY,
    cursor          text NOT NULL DEFAULT '',
    last_status     text NOT NULL DEFAULT '',
    last_error      text NOT NULL DEFAULT '',
    last_polled_at  timestamptz,
    poll_requested  boolean NOT NULL DEFAULT false,
    leader          text NOT NULL DEFAULT '',
    consecutive_errors int NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
INSERT INTO ingest_cursors (name) VALUES ('order-api');

-- Which process currently holds each leader role (informational; the lock is the truth).
CREATE TABLE leaders (
    role        text PRIMARY KEY,
    holder      text NOT NULL,
    since       timestamptz NOT NULL DEFAULT now(),
    heartbeat   timestamptz NOT NULL DEFAULT now()
);
