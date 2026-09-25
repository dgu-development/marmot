-- A glossary term is soft-deleted first, which already takes it off the
-- entity_type count; hard-deleting it afterwards took it off again, so the
-- Discover kind counts drifted below the real number. Only a live term counts
-- down on DELETE, and the glossary count is rebuilt from the live terms.
CREATE OR REPLACE FUNCTION search_index_glossary_trigger()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM search_index WHERE type = 'glossary' AND entity_id = OLD.id::text;
        IF OLD.deleted_at IS NULL THEN
            UPDATE summary_counts SET count = count - 1 WHERE dimension = 'entity_type' AND key = 'glossary';
        END IF;
        RETURN OLD;
    END IF;

    IF NEW.deleted_at IS NOT NULL THEN
        DELETE FROM search_index WHERE type = 'glossary' AND entity_id = NEW.id::text;
        IF TG_OP = 'UPDATE' AND OLD.deleted_at IS NULL THEN
            UPDATE summary_counts SET count = count - 1 WHERE dimension = 'entity_type' AND key = 'glossary';
        END IF;
        RETURN NEW;
    END IF;

    INSERT INTO search_index (
        type, entity_id, name, description, search_text, updated_at,
        asset_type, primary_provider, providers, tags, url_path,
        created_at, metadata
    ) VALUES (
        'glossary',
        NEW.id::text,
        NEW.name,
        COALESCE(NEW.definition, NEW.description),
        COALESCE(NEW.search_text, to_tsvector('english', COALESCE(NEW.name, ''))),
        NEW.updated_at,
        NULL, NULL, NULL,
        COALESCE(NEW.tags, '{}'),
        '/glossary/' || NEW.id::text,
        NEW.created_at,
        COALESCE(NEW.metadata, '{}'::jsonb)
    )
    ON CONFLICT (type, entity_id) DO UPDATE SET
        name = EXCLUDED.name,
        description = EXCLUDED.description,
        search_text = EXCLUDED.search_text,
        updated_at = EXCLUDED.updated_at,
        tags = EXCLUDED.tags,
        url_path = EXCLUDED.url_path,
        created_at = EXCLUDED.created_at,
        metadata = EXCLUDED.metadata;

    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND OLD.deleted_at IS NOT NULL) THEN
        INSERT INTO summary_counts (dimension, key, count)
        VALUES ('entity_type', 'glossary', 1)
        ON CONFLICT (dimension, key) DO UPDATE SET count = summary_counts.count + 1;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

UPDATE summary_counts
   SET count = (SELECT count(*) FROM glossary_terms WHERE deleted_at IS NULL)
 WHERE dimension = 'entity_type' AND key = 'glossary';

---- create above / drop below ----

CREATE OR REPLACE FUNCTION search_index_glossary_trigger()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM search_index WHERE type = 'glossary' AND entity_id = OLD.id::text;
        UPDATE summary_counts SET count = count - 1 WHERE dimension = 'entity_type' AND key = 'glossary';
        RETURN OLD;
    END IF;

    IF NEW.deleted_at IS NOT NULL THEN
        DELETE FROM search_index WHERE type = 'glossary' AND entity_id = NEW.id::text;
        IF TG_OP = 'UPDATE' AND OLD.deleted_at IS NULL THEN
            UPDATE summary_counts SET count = count - 1 WHERE dimension = 'entity_type' AND key = 'glossary';
        END IF;
        RETURN NEW;
    END IF;

    INSERT INTO search_index (
        type, entity_id, name, description, search_text, updated_at,
        asset_type, primary_provider, providers, tags, url_path,
        created_at, metadata
    ) VALUES (
        'glossary',
        NEW.id::text,
        NEW.name,
        COALESCE(NEW.definition, NEW.description),
        COALESCE(NEW.search_text, to_tsvector('english', COALESCE(NEW.name, ''))),
        NEW.updated_at,
        NULL, NULL, NULL,
        COALESCE(NEW.tags, '{}'),
        '/glossary/' || NEW.id::text,
        NEW.created_at,
        COALESCE(NEW.metadata, '{}'::jsonb)
    )
    ON CONFLICT (type, entity_id) DO UPDATE SET
        name = EXCLUDED.name,
        description = EXCLUDED.description,
        search_text = EXCLUDED.search_text,
        updated_at = EXCLUDED.updated_at,
        tags = EXCLUDED.tags,
        url_path = EXCLUDED.url_path,
        created_at = EXCLUDED.created_at,
        metadata = EXCLUDED.metadata;

    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND OLD.deleted_at IS NOT NULL) THEN
        INSERT INTO summary_counts (dimension, key, count)
        VALUES ('entity_type', 'glossary', 1)
        ON CONFLICT (dimension, key) DO UPDATE SET count = summary_counts.count + 1;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
