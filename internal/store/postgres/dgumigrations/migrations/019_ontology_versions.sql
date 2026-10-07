-- Published versions of a domain's ontology: its glossary terms and what the domain says about
-- the ontology, as they were, so an undue change can be undone by restoring one.
CREATE TABLE ontology_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    -- Set on the version taken automatically before restoring another one.
    before_restore_of INTEGER,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    snapshot JSONB NOT NULL,
    UNIQUE (domain_id, version)
);

---- create above / drop below ----

DROP TABLE ontology_versions;
