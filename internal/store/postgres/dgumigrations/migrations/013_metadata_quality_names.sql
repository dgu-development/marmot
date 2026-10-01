-- "quality" read as data quality; the audit is about the metadata of the catalog. Rename in place
-- (role_permissions and user roles reference the rows by id, so nothing is re-granted).
UPDATE permissions SET name = 'dgu_metadata_quality_view', resource_type = 'metadata_quality',
       description = 'View the metadata quality audit settings, runs and results'
 WHERE name = 'dgu_quality_view';
UPDATE permissions SET name = 'dgu_metadata_quality_run', resource_type = 'metadata_quality',
       description = 'Start a metadata quality audit run'
 WHERE name = 'dgu_quality_run';
UPDATE permissions SET name = 'dgu_metadata_quality_manage', resource_type = 'metadata_quality',
       description = 'Change the metadata quality audit settings and schedule'
 WHERE name = 'dgu_quality_manage';
UPDATE roles SET name = 'metadata_quality_auditor',
       description = 'Runs the metadata quality audit and changes its settings'
 WHERE name = 'quality_auditor' AND deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM roles WHERE name = 'metadata_quality_auditor' AND deleted_at IS NULL);

---- create above / drop below ----

UPDATE roles SET name = 'quality_auditor',
       description = 'Runs the quality audit and changes its settings'
 WHERE name = 'metadata_quality_auditor' AND deleted_at IS NULL;
UPDATE permissions SET name = 'dgu_quality_view', resource_type = 'quality',
       description = 'View the quality audit settings, runs and results'
 WHERE name = 'dgu_metadata_quality_view';
UPDATE permissions SET name = 'dgu_quality_run', resource_type = 'quality',
       description = 'Start a quality audit run'
 WHERE name = 'dgu_metadata_quality_run';
UPDATE permissions SET name = 'dgu_quality_manage', resource_type = 'quality',
       description = 'Change the quality audit settings and schedule'
 WHERE name = 'dgu_metadata_quality_manage';
