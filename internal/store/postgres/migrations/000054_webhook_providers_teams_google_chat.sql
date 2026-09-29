ALTER TABLE team_webhooks DROP CONSTRAINT IF EXISTS team_webhooks_provider_check;
ALTER TABLE team_webhooks ADD CONSTRAINT team_webhooks_provider_check
    CHECK (provider IN ('slack', 'discord', 'generic', 'teams', 'google_chat'));

---- create above / drop below ----

DELETE FROM team_webhooks WHERE provider IN ('teams', 'google_chat');
ALTER TABLE team_webhooks DROP CONSTRAINT IF EXISTS team_webhooks_provider_check;
ALTER TABLE team_webhooks ADD CONSTRAINT team_webhooks_provider_check
    CHECK (provider IN ('slack', 'discord', 'generic'));
