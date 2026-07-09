CREATE TABLE IF NOT EXISTS push_subscription_targets (
    id TEXT PRIMARY KEY,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_push_subscription_targets_updated_at
    ON push_subscription_targets(updated_at DESC, id ASC);

CREATE INDEX IF NOT EXISTS idx_push_subscription_targets_payload_gin
    ON push_subscription_targets USING GIN (payload jsonb_path_ops);

CREATE INDEX IF NOT EXISTS idx_push_subscription_targets_user_id
    ON push_subscription_targets ((payload->>'userId'));
