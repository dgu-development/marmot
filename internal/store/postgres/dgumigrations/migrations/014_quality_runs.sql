-- Runs of the metadata quality audit and what they found. Nothing here is a catalog asset: the
-- audit history lives in its own tables, away from search, facets and the asset counts.
-- At most one run is "running" at a time; the partial unique index enforces it across replicas.
CREATE TABLE quality_runs (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    trigger            text        NOT NULL CHECK (trigger IN ('manual', 'schedule')),
    triggered_by       text,
    status             text        NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
    settings_version   bigint      NOT NULL,
    settings           jsonb       NOT NULL,
    metamodel_profile  text        NOT NULL DEFAULT '',
    metamodel_version  integer     NOT NULL DEFAULT 0,
    metamodel_hash     text        NOT NULL DEFAULT '',
    processed          integer     NOT NULL DEFAULT 0,
    total              integer     NOT NULL DEFAULT 0,
    last_asset_id      text,
    started_at         timestamptz NOT NULL DEFAULT now(),
    heartbeat_at       timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz,
    error              text,
    summary            jsonb
);

CREATE UNIQUE INDEX quality_runs_single_running ON quality_runs ((true)) WHERE status = 'running';
CREATE INDEX quality_runs_started_idx ON quality_runs (started_at DESC);

-- No foreign key to assets: the score history of an asset outlives the asset.
CREATE TABLE quality_results (
    run_id        uuid             NOT NULL REFERENCES quality_runs(id) ON DELETE CASCADE,
    asset_id      varchar(255)     NOT NULL,
    asset_mrn     text             NOT NULL DEFAULT '',
    asset_name    text             NOT NULL DEFAULT '',
    asset_type    text             NOT NULL DEFAULT '',
    domain_id     text             NOT NULL DEFAULT 'unassigned',
    completeness  double precision NOT NULL,
    conformity    double precision NOT NULL,
    quality       double precision NOT NULL,
    status        text             NOT NULL CHECK (status IN ('compliant', 'warning', 'noncompliant')),
    issue_count   integer          NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, asset_id)
);

CREATE INDEX quality_results_asset_idx ON quality_results (asset_id);
CREATE INDEX quality_results_run_quality_idx ON quality_results (run_id, quality);

-- Detail of the findings; only the last successful run keeps it.
CREATE TABLE quality_issues (
    run_id    uuid         NOT NULL REFERENCES quality_runs(id) ON DELETE CASCADE,
    asset_id  varchar(255) NOT NULL,
    field_id  text         NOT NULL,
    code      text         NOT NULL,
    rule_id   text         NOT NULL,
    severity  text         NOT NULL CHECK (severity IN ('error', 'warning')),
    section   text         NOT NULL DEFAULT '',
    item      integer
);

CREATE INDEX quality_issues_run_asset_idx ON quality_issues (run_id, asset_id);

---- create above / drop below ----

DROP TABLE quality_issues;
DROP TABLE quality_results;
DROP TABLE quality_runs;
