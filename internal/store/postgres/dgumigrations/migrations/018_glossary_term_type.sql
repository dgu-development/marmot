-- Terms without a type are business terms. Acronyms already stored are left as they are.
UPDATE glossary_terms
   SET metadata = CASE
                    WHEN jsonb_typeof(metadata) = 'object' THEN metadata
                    ELSE '{}'::jsonb
                  END
                || jsonb_build_object(
                     'dgu',
                     CASE
                       WHEN jsonb_typeof(metadata->'dgu') = 'object' THEN metadata->'dgu'
                       ELSE '{}'::jsonb
                     END || '{"term_type":"business_term"}'::jsonb)
 WHERE COALESCE(metadata #>> '{dgu,term_type}', '') = '';

---- create above / drop below ----

-- Empty and business_term are no longer distinct, so this backfill is not reversed.
SELECT 1;
