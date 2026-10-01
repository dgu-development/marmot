-- Settings of the quality audit: one versioned row, written with compare-and-set.
-- The id column and its check keep the table at a single row.
CREATE TABLE quality_settings (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version    bigint      NOT NULL DEFAULT 1,
    settings   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text
);

-- Names carry a dgu_ prefix, as the domain permissions do: permissions.name is
-- unique, and an upstream migration adding the same name later would fail on
-- databases that already have this row. Checks use resource_type and action.
INSERT INTO permissions (name, description, resource_type, action) VALUES
('dgu_quality_view', 'View the quality audit settings, runs and results', 'quality', 'view'),
('dgu_quality_run', 'Start a quality audit run', 'quality', 'run'),
('dgu_quality_manage', 'Change the quality audit settings and schedule', 'quality', 'manage');

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'admin' AND deleted_at IS NULL), id
  FROM permissions
 WHERE name IN ('dgu_quality_view', 'dgu_quality_run', 'dgu_quality_manage');

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'user' AND deleted_at IS NULL), id
  FROM permissions
 WHERE name = 'dgu_quality_view';

-- An ordinary, editable role: whoever holds it is an auditor, and an
-- administrator decides who does from the usual user administration.
INSERT INTO roles (name, description)
SELECT 'quality_auditor', 'Runs the quality audit and changes its settings'
 WHERE NOT EXISTS (SELECT 1 FROM roles WHERE name = 'quality_auditor' AND deleted_at IS NULL);

INSERT INTO role_permissions (role_id, permission_id)
SELECT (SELECT id FROM roles WHERE name = 'quality_auditor' AND deleted_at IS NULL), id
  FROM permissions
 WHERE name IN ('dgu_quality_view', 'dgu_quality_run', 'dgu_quality_manage');

---- create above / drop below ----

DELETE FROM roles WHERE name = 'quality_auditor' AND deleted_at IS NULL;
DELETE FROM role_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE name IN ('dgu_quality_view', 'dgu_quality_run', 'dgu_quality_manage'));
DELETE FROM permissions WHERE name IN ('dgu_quality_view', 'dgu_quality_run', 'dgu_quality_manage');
DROP TABLE quality_settings;
