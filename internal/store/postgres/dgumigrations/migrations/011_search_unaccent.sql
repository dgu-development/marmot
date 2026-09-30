-- Accent-insensitive search. Core indexes every searchable text with the
-- 'english' configuration, which neither removes accents nor stems anything but
-- English, and compares contains/wildcard with ILIKE, which is case- but not
-- accent-insensitive. In Spanish, Portuguese, French... "ordenes" did not find
-- "órdenes" and "jose" did not find "José".
--
--   * dgu_search is a text search configuration that strips accents and then
--     indexes each word as written (no stemming), so it serves any language.
--     Operators can chain a stemmer in after unaccent with ALTER TEXT SEARCH
--     CONFIGURATION, then rebuild the vectors (UPDATE t SET name = name).
--   * dgu_unaccent() is an IMMUTABLE wrapper over unaccent(), which Postgres
--     marks STABLE and therefore refuses to index. Queries that compare text
--     ignoring accents call it on both sides, so trigram and prefix indexes can
--     be built over the same expression.
--
-- The generated search_text columns cannot change their expression in place:
-- they are dropped and added again, which rewrites each table under an
-- exclusive lock. Plan a window for catalogues with millions of rows. A
-- column rebuild fires no row trigger, so search_index is refreshed below.

CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public;

CREATE OR REPLACE FUNCTION public.dgu_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;

CREATE TEXT SEARCH CONFIGURATION public.dgu_search (COPY = pg_catalog.simple);
ALTER TEXT SEARCH CONFIGURATION public.dgu_search
    ALTER MAPPING FOR hword, hword_part, word WITH public.unaccent, pg_catalog.simple;

ALTER TABLE assets DROP COLUMN search_text;
ALTER TABLE assets ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(mrn, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(type, '')), 'B') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(array_to_text(providers), '')), 'B') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(description, '')), 'C')
    ) STORED;
CREATE INDEX idx_assets_search ON assets USING gin(search_text) WHERE is_stub = false;
CREATE INDEX idx_assets_search_all ON assets USING gin(search_text);

ALTER TABLE glossary_terms DROP COLUMN search_text;
ALTER TABLE glossary_terms ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(metadata->'synonyms', '[]'::jsonb)), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(definition, '')), 'B') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(user_definition, '')), 'B') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(description, '')), 'C') ||
        setweight(array_to_tsvector(COALESCE(tags, '{}')), 'C')
    ) STORED;
CREATE INDEX idx_glossary_terms_search ON glossary_terms USING gin(search_text);

ALTER TABLE data_products DROP COLUMN search_text;
ALTER TABLE data_products ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(description, '')), 'B')
    ) STORED;
CREATE INDEX idx_data_products_search ON data_products USING gin(search_text);

ALTER TABLE asset_rules DROP COLUMN search_text;
ALTER TABLE asset_rules ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(description, '')), 'B')
    ) STORED;
CREATE INDEX idx_asset_rules_search ON asset_rules USING gin(search_text);

ALTER TABLE teams DROP COLUMN search_text;
ALTER TABLE teams ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(description, '')), 'B') ||
        setweight(array_to_tsvector(tags), 'C')
    ) STORED;
CREATE INDEX idx_teams_search ON teams USING gin(search_text);

ALTER TABLE users DROP COLUMN search_text;
ALTER TABLE users ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(username, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(name, '')), 'A')
    ) STORED;
CREATE INDEX idx_users_search ON users USING gin(search_text);

ALTER TABLE doc_pages DROP COLUMN search_text;
ALTER TABLE doc_pages ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('public.dgu_search', COALESCE(title, '')), 'A') ||
        setweight(to_tsvector('public.dgu_search', COALESCE(content, '')), 'B')
    ) STORED;
CREATE INDEX idx_doc_pages_search ON doc_pages USING gin(search_text);

-- The sync triggers fall back to an 'english' vector of the name when a row has
-- no search_text. Re-create them from their own current bodies so a later core
-- change to them is kept.
DO $$
DECLARE
    fn regproc;
BEGIN
    FOREACH fn IN ARRAY ARRAY[
        'search_index_asset_trigger'::regproc,
        'search_index_glossary_trigger'::regproc,
        'search_index_team_trigger'::regproc,
        'search_index_data_product_trigger'::regproc
    ] LOOP
        EXECUTE replace(pg_get_functiondef(fn), '''english''', '''public.dgu_search''');
    END LOOP;
END $$;

UPDATE search_index si SET search_text = a.search_text
  FROM assets a WHERE si.type = 'asset' AND si.entity_id = a.id::text;
UPDATE search_index si SET search_text = g.search_text
  FROM glossary_terms g WHERE si.type = 'glossary' AND si.entity_id = g.id::text;
UPDATE search_index si SET search_text = t.search_text
  FROM teams t WHERE si.type = 'team' AND si.entity_id = t.id::text;
UPDATE search_index si SET search_text = d.search_text
  FROM data_products d WHERE si.type = 'data_product' AND si.entity_id = d.id::text;

-- Trigram and prefix indexes over the unaccented name, which is what the
-- queries now compare. Assets have no trigram index by design (core 000032).
DROP INDEX IF EXISTS idx_search_index_name_trgm;
DROP INDEX IF EXISTS idx_data_products_name_trgm;
DROP INDEX IF EXISTS idx_asset_rules_name_trgm;
DROP INDEX IF EXISTS idx_search_index_name_prefix;
CREATE INDEX idx_search_index_name_trgm ON search_index USING gin (dgu_unaccent(name) gin_trgm_ops);
CREATE INDEX idx_data_products_name_trgm ON data_products USING gin (dgu_unaccent(name) gin_trgm_ops);
CREATE INDEX idx_asset_rules_name_trgm ON asset_rules USING gin (dgu_unaccent(name) gin_trgm_ops);
CREATE INDEX idx_search_index_name_prefix ON search_index
    USING btree (type, dgu_unaccent(lower(name)) text_pattern_ops)
    INCLUDE (entity_id, description, url_path, updated_at, asset_type, primary_provider);

---- create above / drop below ----

DROP INDEX IF EXISTS idx_search_index_name_trgm;
DROP INDEX IF EXISTS idx_data_products_name_trgm;
DROP INDEX IF EXISTS idx_asset_rules_name_trgm;
DROP INDEX IF EXISTS idx_search_index_name_prefix;
CREATE INDEX idx_search_index_name_trgm ON search_index USING gin (name gin_trgm_ops);
CREATE INDEX idx_data_products_name_trgm ON data_products USING gin (name gin_trgm_ops);
CREATE INDEX idx_asset_rules_name_trgm ON asset_rules USING gin (name gin_trgm_ops);
CREATE INDEX idx_search_index_name_prefix ON search_index
    USING btree (type, lower(name) text_pattern_ops)
    INCLUDE (entity_id, description, url_path, updated_at, asset_type, primary_provider);

DO $$
DECLARE
    fn regproc;
BEGIN
    FOREACH fn IN ARRAY ARRAY[
        'search_index_asset_trigger'::regproc,
        'search_index_glossary_trigger'::regproc,
        'search_index_team_trigger'::regproc,
        'search_index_data_product_trigger'::regproc
    ] LOOP
        EXECUTE replace(pg_get_functiondef(fn), '''public.dgu_search''', '''english''');
    END LOOP;
END $$;

ALTER TABLE assets DROP COLUMN search_text;
ALTER TABLE assets ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(mrn, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(type, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(array_to_text(providers), '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'C')
    ) STORED;
CREATE INDEX idx_assets_search ON assets USING gin(search_text) WHERE is_stub = false;
CREATE INDEX idx_assets_search_all ON assets USING gin(search_text);

ALTER TABLE glossary_terms DROP COLUMN search_text;
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

ALTER TABLE data_products DROP COLUMN search_text;
ALTER TABLE data_products ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'B')
    ) STORED;
CREATE INDEX idx_data_products_search ON data_products USING gin(search_text);

ALTER TABLE asset_rules DROP COLUMN search_text;
ALTER TABLE asset_rules ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'B')
    ) STORED;
CREATE INDEX idx_asset_rules_search ON asset_rules USING gin(search_text);

ALTER TABLE teams DROP COLUMN search_text;
ALTER TABLE teams ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
        setweight(array_to_tsvector(tags), 'C')
    ) STORED;
CREATE INDEX idx_teams_search ON teams USING gin(search_text);

ALTER TABLE users DROP COLUMN search_text;
ALTER TABLE users ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(username, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(name, '')), 'A')
    ) STORED;
CREATE INDEX idx_users_search ON users USING gin(search_text);

ALTER TABLE doc_pages DROP COLUMN search_text;
ALTER TABLE doc_pages ADD COLUMN search_text tsvector
    GENERATED ALWAYS AS (
        setweight(to_tsvector('english', COALESCE(title, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(content, '')), 'B')
    ) STORED;
CREATE INDEX idx_doc_pages_search ON doc_pages USING gin(search_text);

UPDATE search_index si SET search_text = a.search_text
  FROM assets a WHERE si.type = 'asset' AND si.entity_id = a.id::text;
UPDATE search_index si SET search_text = g.search_text
  FROM glossary_terms g WHERE si.type = 'glossary' AND si.entity_id = g.id::text;
UPDATE search_index si SET search_text = t.search_text
  FROM teams t WHERE si.type = 'team' AND si.entity_id = t.id::text;
UPDATE search_index si SET search_text = d.search_text
  FROM data_products d WHERE si.type = 'data_product' AND si.entity_id = d.id::text;

DROP TEXT SEARCH CONFIGURATION public.dgu_search;
DROP FUNCTION public.dgu_unaccent(text);
