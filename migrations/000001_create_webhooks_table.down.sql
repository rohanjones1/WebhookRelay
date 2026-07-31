DROP TRIGGER IF EXISTS trg_webhooks_updated_at ON webhooks;
DROP FUNCTION IF EXISTS set_updated_at();

DROP INDEX IF EXISTS idx_webhooks_target_url;
DROP INDEX IF EXISTS idx_webhooks_status_next_attempt;

DROP TABLE IF EXISTS webhooks;

DROP TYPE IF EXISTS webhook_status;
