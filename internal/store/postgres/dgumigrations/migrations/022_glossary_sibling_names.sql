-- Two live terms under the same parent cannot share a name, whatever the case: the second one is
-- what an ingestion never writes to and what a person picks by mistake. Terms that already
-- collide keep the oldest as it is and number the rest, so nobody loses a term and the repeats
-- are in sight to merge or rename.
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY parent_term_id, lower(name) ORDER BY created_at, id) AS n
      FROM glossary_terms
     WHERE deleted_at IS NULL
)
UPDATE glossary_terms t
   SET name = t.name || ' (' || r.n || ')'
  FROM ranked r
 WHERE r.id = t.id AND r.n > 1;

CREATE UNIQUE INDEX glossary_terms_sibling_name_key
    ON glossary_terms (COALESCE(parent_term_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name))
 WHERE deleted_at IS NULL;

---- create above / drop below ----

DROP INDEX glossary_terms_sibling_name_key;
