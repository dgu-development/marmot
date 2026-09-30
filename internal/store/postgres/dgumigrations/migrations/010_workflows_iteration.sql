-- Same naming rule as 002 and 009: dgu_ prefix, checks use resource_type and action.
-- Starting a workflow is a permission of its own, granted to no built-in role but admin:
-- the built-in user role is a system role and cannot be edited, so an administrator gives
-- it to whoever should start flows through a role of their own.
INSERT INTO permissions (name, description, resource_type, action) VALUES
('dgu_start_workflows', 'Start workflow runs, alone or in a batch', 'workflows', 'start');

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'admin' AND deleted_at IS NULL), id
  FROM permissions
 WHERE name = 'dgu_start_workflows';

-- A task keeps the form fields its node declared when it opened, so listing an inbox
-- reads the row instead of parsing the diagram of every task, and a reminder is
-- scheduled once, when the task opens.
ALTER TABLE workflow_tasks
    ADD COLUMN form_fields TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN remind_at TIMESTAMPTZ,
    ADD COLUMN reminded BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX workflow_tasks_remind_idx ON workflow_tasks (remind_at)
    WHERE status = 'open' AND reminded = false AND remind_at IS NOT NULL;

-- A published definition whose start event is a timer: the ticker starts it when next_run_at
-- passes, as run_as, the person who published it, with that person's permissions at that moment.
CREATE TABLE workflow_schedules (
    definition_id UUID PRIMARY KEY REFERENCES workflow_definitions(id) ON DELETE CASCADE,
    cycle TEXT NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    run_as UUID REFERENCES users(id) ON DELETE SET NULL,
    next_run_at TIMESTAMPTZ NOT NULL,
    last_run_at TIMESTAMPTZ,
    last_error TEXT,
    enabled BOOLEAN NOT NULL DEFAULT true
);
CREATE INDEX workflow_schedules_due_idx ON workflow_schedules (next_run_at) WHERE enabled;

---- create above / drop below ----

DROP TABLE workflow_schedules;
DROP INDEX workflow_tasks_remind_idx;
ALTER TABLE workflow_tasks DROP COLUMN form_fields, DROP COLUMN remind_at, DROP COLUMN reminded;

DELETE FROM role_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'dgu_start_workflows');
DELETE FROM permissions WHERE name = 'dgu_start_workflows';
