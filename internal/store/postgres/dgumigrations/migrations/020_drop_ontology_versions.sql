-- Ontology versions moved to the dguext-ontology extension, which keeps them in
-- ext_ontology_versions. Versions published here before the move are not carried over.
DROP TABLE ontology_versions;

---- create above / drop below ----

CREATE TABLE ontology_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    before_restore_of INTEGER,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    snapshot JSONB NOT NULL,
    UNIQUE (domain_id, version)
);
