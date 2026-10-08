-- The score the audit writes on each asset measures its metadata, not its data: the fields take
-- the metadata_ prefix, and the values already stored move with them.
UPDATE assets
   SET metadata = jsonb_set(
         metadata, '{dgu}',
         ((metadata->'dgu') - 'quality_score' - 'quality_dimensions' - 'quality_evaluated_at')
         || jsonb_strip_nulls(jsonb_build_object(
              'metadata_quality_score', metadata->'dgu'->'quality_score',
              'metadata_quality_dimensions', metadata->'dgu'->'quality_dimensions',
              'metadata_quality_evaluated_at', metadata->'dgu'->'quality_evaluated_at')))
 WHERE jsonb_typeof(metadata->'dgu') = 'object'
   AND metadata->'dgu' ?| ARRAY['quality_score', 'quality_dimensions', 'quality_evaluated_at'];

---- create above / drop below ----

UPDATE assets
   SET metadata = jsonb_set(
         metadata, '{dgu}',
         ((metadata->'dgu') - 'metadata_quality_score' - 'metadata_quality_dimensions' - 'metadata_quality_evaluated_at')
         || jsonb_strip_nulls(jsonb_build_object(
              'quality_score', metadata->'dgu'->'metadata_quality_score',
              'quality_dimensions', metadata->'dgu'->'metadata_quality_dimensions',
              'quality_evaluated_at', metadata->'dgu'->'metadata_quality_evaluated_at')))
 WHERE jsonb_typeof(metadata->'dgu') = 'object'
   AND metadata->'dgu' ?| ARRAY['metadata_quality_score', 'metadata_quality_dimensions', 'metadata_quality_evaluated_at'];
