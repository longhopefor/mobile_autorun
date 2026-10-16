CREATE TABLE business_records (
 id text PRIMARY KEY, owner text NOT NULL REFERENCES users(id), task_id text NOT NULL REFERENCES tasks(id),
 platform text NOT NULL, account text NOT NULL, kind text NOT NULL, record_key text NOT NULL,
 data jsonb NOT NULL, observation_id text NOT NULL REFERENCES observations(id), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner,platform,account,kind,record_key)
);
CREATE INDEX business_records_owner_idx ON business_records(owner,kind,updated_at DESC);
CREATE TABLE business_outreach (
 id text PRIMARY KEY, owner text NOT NULL REFERENCES users(id), task_id text NOT NULL REFERENCES tasks(id),
 platform text NOT NULL, account text NOT NULL, recipient text NOT NULL, message_key text NOT NULL,
 data jsonb NOT NULL, status text NOT NULL CHECK(status IN ('reserved','reported','uncertain')),
 observation_id text NOT NULL REFERENCES observations(id), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner,platform,account,message_key)
);
CREATE TABLE material_workflows (
 id text PRIMARY KEY, owner text NOT NULL REFERENCES users(id), idempotency_key text NOT NULL, request_hash text NOT NULL,
 spec jsonb NOT NULL, result jsonb NOT NULL DEFAULT '{}', status text NOT NULL DEFAULT 'queued',
 error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(owner,idempotency_key)
);
CREATE TABLE business_campaigns (
 id text PRIMARY KEY, owner text NOT NULL REFERENCES users(id), name text NOT NULL, spec jsonb NOT NULL,
 interval_minutes integer NOT NULL CHECK(interval_minutes BETWEEN 15 AND 10080), enabled boolean NOT NULL DEFAULT true,
 next_run timestamptz NOT NULL, last_task_id text REFERENCES tasks(id), error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now()
);
