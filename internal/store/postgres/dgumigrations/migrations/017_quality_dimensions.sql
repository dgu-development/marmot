-- Scores by quality dimension. The results keep one score per dimension that applied to the asset
-- instead of the two fixed columns; scores from before are not comparable (the weights now spread
-- over four dimensions), so the earlier columns are dropped with them.
ALTER TABLE quality_results ADD COLUMN scores jsonb NOT NULL DEFAULT '{}';
ALTER TABLE quality_results DROP COLUMN completeness, DROP COLUMN conformity;

-- Weights saved (and those of the runs already made) were two: spread them as the defaults do.
UPDATE quality_settings
   SET settings = jsonb_set(settings, '{weights}', '{"completeness":30,"validity":30,"consistency":20,"timeliness":20}');
UPDATE quality_runs
   SET settings = jsonb_set(settings, '{weights}', '{"completeness":30,"validity":30,"consistency":20,"timeliness":20}');

-- A rule written before dimensions existed judged the validity of a value.
UPDATE quality_custom_rules
   SET rule = jsonb_set(rule, '{dimension}', '"validity"')
 WHERE NOT rule ? 'dimension';

---- create above / drop below ----

ALTER TABLE quality_results ADD COLUMN completeness double precision NOT NULL DEFAULT 0,
                            ADD COLUMN conformity double precision NOT NULL DEFAULT 0;
ALTER TABLE quality_results DROP COLUMN scores;
