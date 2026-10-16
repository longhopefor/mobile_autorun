CREATE TABLE IF NOT EXISTS business_progress (
 task_id text PRIMARY KEY REFERENCES tasks(id),
 state jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
