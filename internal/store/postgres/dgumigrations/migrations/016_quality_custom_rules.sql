-- Quality rules written in the interface, next to the ones the metamodel profile declares. The rule
-- is a closed, declarative definition (conditions over profile fields), never code or SQL.
CREATE TABLE quality_custom_rules (
    id         text        PRIMARY KEY,
    version    bigint      NOT NULL DEFAULT 1,
    enabled    boolean     NOT NULL DEFAULT true,
    rule       jsonb       NOT NULL,
    created_by text,
    updated_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The custom rules a run applied, as they were when it started: rules change and runs are history.
ALTER TABLE quality_runs ADD COLUMN custom_rules jsonb;

---- create above / drop below ----

ALTER TABLE quality_runs DROP COLUMN custom_rules;
DROP TABLE quality_custom_rules;
