-- Governance workflows (BPMN subset). A definition is a versioned BPMN
-- document; instances pin the version they started on.
CREATE TABLE workflow_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    process_key TEXT NOT NULL,
    name TEXT NOT NULL,
    version INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'retired')),
    bpmn TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    UNIQUE (process_key, version)
);

CREATE TABLE workflow_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    definition_id UUID NOT NULL REFERENCES workflow_definitions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
    target_kind TEXT,
    target_id TEXT,
    target_name TEXT,
    state JSONB NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    initiator_id UUID REFERENCES users(id) ON DELETE SET NULL,
    failure_code TEXT,
    failure_element TEXT,
    failure_detail TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ
);
CREATE INDEX workflow_instances_definition_idx ON workflow_instances (definition_id);
CREATE INDEX workflow_instances_target_idx ON workflow_instances (target_kind, target_id);
CREATE INDEX workflow_instances_initiator_idx ON workflow_instances (initiator_id);

-- candidates is the assignment resolved when the task was created, for the
-- inbox. Completing a task resolves it again, so a revoked role cannot decide.
CREATE TABLE workflow_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id UUID NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
    token_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'completed', 'cancelled')),
    candidates UUID[] NOT NULL DEFAULT '{}',
    due_at TIMESTAMPTZ,
    timer_node TEXT,
    timer_fired BOOLEAN NOT NULL DEFAULT false,
    decision TEXT,
    comment TEXT,
    completed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (instance_id, token_id)
);
CREATE INDEX workflow_tasks_candidates_idx ON workflow_tasks USING GIN (candidates) WHERE status = 'open';
CREATE INDEX workflow_tasks_due_idx ON workflow_tasks (due_at) WHERE status = 'open' AND timer_fired = false;

CREATE TABLE workflow_events (
    id BIGSERIAL PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    element TEXT,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    detail JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX workflow_events_instance_idx ON workflow_events (instance_id, id);

-- Same naming rule as 002: dgu_ prefix, checks use resource_type and action.
INSERT INTO permissions (name, description, resource_type, action) VALUES
('dgu_view_workflows', 'Start workflows, see your own runs and decide your tasks', 'workflows', 'view'),
('dgu_manage_workflows', 'Create, publish and retire workflow definitions, and see and cancel every run', 'workflows', 'manage');

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'admin'), id
  FROM permissions
 WHERE name IN ('dgu_view_workflows', 'dgu_manage_workflows');

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'user'), id
  FROM permissions
 WHERE name = 'dgu_view_workflows';

---- create above / drop below ----

DELETE FROM role_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE name IN ('dgu_view_workflows', 'dgu_manage_workflows'));
DELETE FROM permissions WHERE name IN ('dgu_view_workflows', 'dgu_manage_workflows');
DROP TABLE workflow_events;
DROP TABLE workflow_tasks;
DROP TABLE workflow_instances;
DROP TABLE workflow_definitions;
