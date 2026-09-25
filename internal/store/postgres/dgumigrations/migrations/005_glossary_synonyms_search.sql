-- Index a term's synonyms (metadata.synonyms, where discovery runs and the
-- metamodel store them) with the weight of its name, so searching an
-- alternative name finds the term. search_text is generated, so it is
-- rebuilt the way core 000050 did.
--
-- COALESCE(tags, '{}') fixes a core defect on the way: the built-in
-- array_to_tsvector returns NULL for a NULL array, which left every term
-- without tags with no full-text vector at all.
DROP INDEX IF EXISTS idx_glossary_terms_search;
ALTER TABLE glossary_terms DROP COLUMN IF EXISTS search_text;
ALTER TABLE glossary_terms ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(metadata->'synonyms', '[]'::jsonb)), 'A') ||
        setweight(to_tsvector('english', COALESCE(definition, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(user_definition, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'C') ||
        setweight(array_to_tsvector(COALESCE(tags, '{}')), 'C')
    ) STORED;
CREATE INDEX idx_glossary_terms_search ON glossary_terms USING gin(search_text);

-- Rebuilding a column fires no row trigger, so search_index keeps the old vectors.
UPDATE search_index si
   SET search_text = g.search_text
  FROM glossary_terms g
 WHERE si.type = 'glossary' AND si.entity_id = g.id::text;

---- create above / drop below ----

DROP INDEX IF EXISTS idx_glossary_terms_search;
ALTER TABLE glossary_terms DROP COLUMN IF EXISTS search_text;
ALTER TABLE glossary_terms ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(definition, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(user_definition, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'C') ||
        setweight(array_to_tsvector(tags), 'C')
    ) STORED;
CREATE INDEX idx_glossary_terms_search ON glossary_terms USING gin(search_text);

UPDATE search_index si
   SET search_text = COALESCE(g.search_text, to_tsvector('english', COALESCE(g.name, '')))
  FROM glossary_terms g
 WHERE si.type = 'glossary' AND si.entity_id = g.id::text;
