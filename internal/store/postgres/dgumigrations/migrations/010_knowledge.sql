CREATE TABLE knowledge_pages (
    entity_type TEXT NOT NULL CHECK (entity_type IN ('asset', 'data_product', 'glossary_term', 'domain')),
    entity_id TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    source_hash TEXT NOT NULL DEFAULT '',
    sources JSONB NOT NULL DEFAULT '[]',
    mode TEXT NOT NULL DEFAULT 'extractive',
    published_at TIMESTAMPTZ,
    published_by TEXT,
    draft_content TEXT NOT NULL DEFAULT '',
    draft_hash TEXT NOT NULL DEFAULT '',
    draft_source_hash TEXT NOT NULL DEFAULT '',
    draft_sources JSONB NOT NULL DEFAULT '[]',
    draft_mode TEXT NOT NULL DEFAULT 'extractive',
    draft_compiler_hash TEXT NOT NULL DEFAULT '',
    compiled_at TIMESTAMPTZ,
    PRIMARY KEY (entity_type, entity_id)
);

INSERT INTO permissions (name, description, resource_type, action)
VALUES ('dgu_write_knowledge', 'Compile and publish evidence-backed knowledge', 'knowledge', 'write');
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin' AND p.name = 'dgu_write_knowledge';

---- create above / drop below ----

DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name = 'dgu_write_knowledge');
DELETE FROM permissions WHERE name = 'dgu_write_knowledge';
DROP TABLE knowledge_pages;
