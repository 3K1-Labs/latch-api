DROP INDEX IF EXISTS idx_push_devices_wallet_token;
DROP INDEX IF EXISTS idx_push_devices_wallet_address;

ALTER TABLE push_devices
    DROP CONSTRAINT IF EXISTS push_devices_subject_check,
    DROP COLUMN IF EXISTS wallet_address,
    ALTER COLUMN user_id SET NOT NULL;

DROP INDEX IF EXISTS idx_notifications_wallet_dedupe;
DROP INDEX IF EXISTS idx_notifications_wallet_unread;
DROP INDEX IF EXISTS idx_notifications_wallet_created;

ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_subject_check,
    DROP COLUMN IF EXISTS wallet_address,
    ALTER COLUMN user_id SET NOT NULL;
