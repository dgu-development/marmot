-- How the writing of the scores went in each run: written, skipped because the asset was edited
-- after the audit read it, and failed.
ALTER TABLE quality_runs
    ADD COLUMN scores_written  integer NOT NULL DEFAULT 0,
    ADD COLUMN score_conflicts integer NOT NULL DEFAULT 0,
    ADD COLUMN score_failures  integer NOT NULL DEFAULT 0;

---- create above / drop below ----

ALTER TABLE quality_runs
    DROP COLUMN scores_written,
    DROP COLUMN score_conflicts,
    DROP COLUMN score_failures;
