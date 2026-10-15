-- Generic per-user push device registry (mobile only). Distinct from
-- cosign_push_tokens, which is intentionally keyed by a blind queue/signer id
-- with no user_id link — this table is the opposite: a plain per-user device
-- registration for the general activity-notification push channel.
CREATE TABLE push_devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    push_token    TEXT NOT NULL,
    platform      VARCHAR(16) NOT NULL DEFAULT 'expo',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, push_token)
);

CREATE INDEX idx_push_devices_user_id ON push_devices(user_id);

-- Mobile activity notifications (curated event set — see
-- internal/service/notification_service.go). dedupe_key + the partial unique
-- index below make writes idempotent for events observed on every client
-- poll (e.g. funding completion), so a repeated poll after the first write is
-- a free no-op and push fires exactly once.
CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        VARCHAR(64) NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    metadata    JSONB,
    dedupe_key  TEXT,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);
CREATE INDEX idx_notifications_user_unread ON notifications(user_id) WHERE read_at IS NULL;
CREATE UNIQUE INDEX idx_notifications_dedupe ON notifications(user_id, type, dedupe_key) WHERE dedupe_key IS NOT NULL;

-- Webapp activity notifications: in-app only for v1, no push_devices
-- counterpart — Expo push doesn't serve a browser frontend, and real Web
-- Push (VAPID) isn't built yet. Same shape as mobile's notifications table.
CREATE TABLE webapp.notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES webapp.users(id) ON DELETE CASCADE,
    type        VARCHAR(64) NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    metadata    JSONB,
    dedupe_key  TEXT,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webapp_notifications_user_created ON webapp.notifications(user_id, created_at DESC);
CREATE INDEX idx_webapp_notifications_user_unread ON webapp.notifications(user_id) WHERE read_at IS NULL;
CREATE UNIQUE INDEX idx_webapp_notifications_dedupe ON webapp.notifications(user_id, type, dedupe_key) WHERE dedupe_key IS NOT NULL;
