package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

const maxSourceBytes = 512 * 1024
const maxSources = 1000

var entityTables = map[string]string{"asset": "assets", "data_product": "data_products", "glossary_term": "glossary_terms", "domain": "domains"}

func (s *Service) valid(e Entity) bool {
	return entityTables[e.Kind] != "" && e.ID != "" && len(e.ID) <= 255 && (e.Kind != "domain" || s.opts.DomainsEnabled)
}

func entityURL(e Entity) string {
	switch e.Kind {
	case "asset":
		return "/discover?q=" + url.QueryEscape(e.ID)
	case "data_product":
		return "/products/" + url.PathEscape(e.ID)
	case "glossary_term":
		return "/glossary/" + url.PathEscape(e.ID)
	default:
		return "/domains/" + url.PathEscape(e.ID)
	}
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sourceHash(sources []Source) string {
	copy := append([]Source(nil), sources...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].ID < copy[j].ID })
	return hash(copy)
}

func (s *Service) snapshot(ctx context.Context, tx pgx.Tx, e Entity) (*Page, error) {
	if !s.valid(e) {
		return nil, ErrInvalid
	}
	filter := ""
	if e.Kind == "glossary_term" {
		filter = " AND deleted_at IS NULL"
	}
	if e.Kind == "asset" {
		filter = " AND is_stub = FALSE"
	}
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT to_jsonb(t) FROM "+entityTables[e.Kind]+" t WHERE id::text = $1"+filter, e.ID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read knowledge entity: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	p := &Page{Entity: e, EntityURL: entityURL(e), DocEntityID: e.ID, Sources: []Source{}, PublishedSources: []Source{}, DraftSources: []Source{}, Status: "uncompiled", Freshness: "uncompiled", DraftFreshness: "uncompiled", Mode: "extractive", DraftMode: "extractive"}
	_ = json.Unmarshal(fields["name"], &p.Title)
	for _, field := range []string{"user_description", "description", "user_definition", "definition"} {
		if json.Unmarshal(fields[field], &p.Description) == nil && strings.TrimSpace(p.Description) != "" {
			break
		}
	}
	if e.Kind == "asset" {
		var mrn string
		_ = json.Unmarshal(fields["mrn"], &mrn)
		p.EntityURL = "/discover?q=" + url.QueryEscape(mrn)
		p.DocEntityID = mrn
	}
	size := 0
	add := func(id, title, link, field, text string) error {
		size += len(text)
		if size > maxSourceBytes || len(p.Sources) >= maxSources {
			return ErrTooLarge
		}
		preview := []rune(strings.Join(strings.Fields(text), " "))
		if len(preview) > 220 {
			preview = append(preview[:220], '…')
		}
		p.Sources = append(p.Sources, Source{ID: id, Title: title, URL: link, Field: field, Hash: hash(text), Preview: string(preview), Text: text})
		return nil
	}
	// Explicit fields keep operational timestamps, popularity and search indexes out of evidence.
	for _, field := range []string{"name", "mrn", "type", "providers", "environments", "description", "user_description", "definition", "user_definition", "metadata", "schema", "tags", "synonyms", "parent_term_id", "parent_id", "restricted"} {
		value, ok := fields[field]
		if !ok || string(value) == "null" || string(value) == `""` || string(value) == "{}" || string(value) == "[]" {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		if err := add(e.Kind+":"+e.ID+":"+field, field, p.EntityURL, field, text); err != nil {
			return nil, err
		}
	}
	if len(s.opts.Schema) > 0 {
		var schema any
		if json.Unmarshal(s.opts.Schema, &schema) != nil {
			return nil, fmt.Errorf("%w: invalid metamodel schema", ErrInvalid)
		}
		text, _ := json.Marshal(schema)
		if err := add("metamodel:schema", "Metamodel", "/api/v1/metamodel", "schema", string(text)); err != nil {
			return nil, err
		}
	}
	if e.Kind == "asset" || e.Kind == "data_product" {
		rows, err := tx.Query(ctx, `SELECT 'doc:'||id::text id,title,coalesce(content,'') FROM doc_pages WHERE entity_type=$1 AND (entity_id=$2 OR entity_id=$3)
 UNION ALL SELECT 'imported-doc:'||d.id::text,d.source,d.content FROM documentation d JOIN assets a ON a.mrn=d.mrn WHERE $1='asset' AND a.id=$2
 UNION SELECT 'global-doc:'||g.id::text,g.source,g.content FROM global_documentation g JOIN documentation d ON g.source=ANY(d.global_docs) JOIN assets a ON a.mrn=d.mrn WHERE $1='asset' AND a.id=$2 ORDER BY id LIMIT 1001`, e.Kind, e.ID, p.DocEntityID)
		if err != nil {
			return nil, fmt.Errorf("read knowledge documentation: %w", err)
		}
		for rows.Next() {
			var id, title, text string
			if err = rows.Scan(&id, &title, &text); err != nil {
				rows.Close()
				return nil, err
			}
			if err = add(id, title, p.EntityURL, "content", text); err != nil {
				rows.Close()
				return nil, err
			}
			if !strings.HasPrefix(id, "doc:") {
				p.Documents = append(p.Documents, Document{ID: id, Title: title, Content: text})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		column := "asset_id"
		if e.Kind == "data_product" {
			column = "data_product_id"
		}
		rows, err = tx.Query(ctx, `SELECT id::text,content,jsonb_build_object('type',updated_by_type,'id',updated_by_id,'name',updated_by_name)::text FROM memories WHERE `+column+`::text=$1 ORDER BY id LIMIT 1001`, e.ID)
		if err != nil {
			return nil, fmt.Errorf("read knowledge memories: %w", err)
		}
		for rows.Next() {
			var id, text, author string
			if err = rows.Scan(&id, &text, &author); err != nil {
				rows.Close()
				return nil, err
			}
			if err = add("memory:"+id, "Memory", p.EntityURL, "content", text+"\nAuthor: "+author); err != nil {
				rows.Close()
				return nil, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, relationsSQL, e.Kind, e.ID, s.opts.DomainsEnabled)
	if err != nil {
		return nil, fmt.Errorf("read knowledge relations: %w", err)
	}
	for rows.Next() {
		var kind, id, title, relation, origin string
		if err = rows.Scan(&kind, &id, &title, &relation, &origin); err != nil {
			rows.Close()
			return nil, err
		}
		target := Entity{Kind: kind, ID: id}
		if err = add("relation:"+relation+":"+kind+":"+id+":"+origin, title, entityURL(target), relation, relation+": "+title+" ("+kind+":"+id+") ["+origin+"]"); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if e.Kind == "glossary_term" && len(s.opts.Schema) > 0 {
		var schema metamodel.Schema
		var metadata map[string]any
		if err = json.Unmarshal(s.opts.Schema, &schema); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(fields["metadata"], &metadata); err != nil {
			return nil, err
		}
		for _, field := range schema.Fields {
			if !schema.Enabled || field.Presentation.Control != metamodel.ControlGlossaryTerm || !strings.HasPrefix(field.Storage, "metadata.") {
				continue
			}
			value, _ := metamodel.ValueAt(metadata, field.Storage)
			ids := glossary.LinkIDs(value)
			path := strings.Split(strings.TrimPrefix(field.Storage, "metadata."), ".")
			rows, err = tx.Query(ctx, `SELECT id::text,name,COALESCE(user_definition,definition),false FROM glossary_terms WHERE deleted_at IS NULL AND id::text=ANY($1::text[])
    UNION ALL SELECT id::text,name,COALESCE(user_definition,definition),true FROM glossary_terms WHERE deleted_at IS NULL AND (metadata #> $2::text[] @> jsonb_build_array($3::text) OR metadata #> $2::text[] = to_jsonb($3::text)) ORDER BY 1,4 LIMIT 1001`, ids, path, e.ID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id, title, definition string
				var inverse bool
				if err = rows.Scan(&id, &title, &definition, &inverse); err != nil {
					rows.Close()
					return nil, err
				}
				relation := field.ID
				if inverse {
					relation = "inverse_" + relation
				}
				if err = add("metamodel-link:"+relation+":"+id, title, entityURL(Entity{Kind: "glossary_term", ID: id}), relation, relation+": "+title+"\n"+definition); err != nil {
					rows.Close()
					return nil, err
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(p.Sources, func(i, j int) bool { return strings.Compare(p.Sources[i].ID, p.Sources[j].ID) < 0 })
	p.CurrentSourceHash = sourceHash(p.Sources)
	return p, nil
}

const relationsSQL = `
WITH links AS (
 SELECT 'asset'::text a_kind, m.asset_id::text a_id, 'data_product'::text b_kind, p.id::text b_id, p.name b_name, a.name a_name, 'member_of'::text rel, m.source::text origin
 FROM data_product_memberships m JOIN assets a ON a.id=m.asset_id AND NOT a.is_stub JOIN data_products p ON p.id=m.data_product_id
 UNION ALL
 SELECT 'asset',t.asset_id,'glossary_term',g.id::text,g.name,a.name,'defined_by',t.source FROM asset_terms t JOIN assets a ON a.id=t.asset_id AND NOT a.is_stub JOIN glossary_terms g ON g.id=t.glossary_term_id AND g.deleted_at IS NULL
 UNION ALL
 SELECT DISTINCT 'asset',a.id,'asset',b.id,b.name,a.name,'flows_to','lineage' FROM lineage_edges l JOIN assets a ON a.mrn=l.source_mrn AND NOT a.is_stub JOIN assets b ON b.mrn=l.target_mrn AND NOT b.is_stub
 UNION ALL
 SELECT 'glossary_term',g.id::text,'glossary_term',p.id::text,p.name,g.name,'child_of','glossary' FROM glossary_terms g JOIN glossary_terms p ON p.id=g.parent_term_id WHERE g.deleted_at IS NULL AND p.deleted_at IS NULL
 UNION ALL
 SELECT 'domain',d.id::text,'domain',p.id::text,p.name,d.name,'child_of','domain' FROM domains d JOIN domains p ON p.id=d.parent_id WHERE $3
 UNION ALL
 SELECT 'asset',a.id,'domain',d.id::text,d.name,a.name,'in_domain','domain' FROM assets a LEFT JOIN asset_domains m ON m.asset_id=a.id JOIN domains d ON d.id=coalesce(m.domain_id,'00000000-0000-4000-8000-000000000001'::uuid) WHERE $3 AND NOT a.is_stub
 UNION ALL
 SELECT 'data_product',p.id::text,'domain',d.id::text,d.name,p.name,'in_domain','domain' FROM data_products p LEFT JOIN data_product_domains m ON m.data_product_id=p.id JOIN domains d ON d.id=coalesce(m.domain_id,'00000000-0000-4000-8000-000000000001'::uuid) WHERE $3
 UNION ALL
 SELECT 'glossary_term',g.id::text,'domain',d.id::text,d.name,g.name,'in_domain','domain' FROM glossary_terms g LEFT JOIN glossary_term_domains m ON m.glossary_term_id=g.id JOIN domains d ON d.id=coalesce(m.domain_id,'00000000-0000-4000-8000-000000000001'::uuid) WHERE $3 AND g.deleted_at IS NULL
), selected AS (
 SELECT b_kind kind,b_id id,b_name title,rel,origin FROM links WHERE a_kind=$1 AND a_id=$2
 UNION ALL
 SELECT a_kind,a_id,a_name,'inverse_'||rel,origin FROM links WHERE b_kind=$1 AND b_id=$2
)
SELECT DISTINCT kind,id,title,rel,origin FROM selected ORDER BY kind,id,rel,origin LIMIT 1001`
