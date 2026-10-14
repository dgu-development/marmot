// Package readscope keeps the content of the domains hidden from a caller out
// of what the stores read. The domain package decides which domains those
// are, but the stores cannot import it, so it installs a Resolver here.
//
// A store reads through DB, whose SELECTs see every table that holds or
// refers to an entity of a domain without the hidden rows, or asks for a
// condition with Cond where the query is built by hand. Without a resolver,
// or for a caller nothing is hidden from, the queries are left as they were.
package readscope

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Kind is an entity kind that belongs to a domain.
type Kind int

const (
	Asset Kind = iota
	GlossaryTerm
	DataProduct
)

// hiddenIDs are subqueries for the IDs of the hidden entities of each kind;
// %[1]d is the parameter with the hidden domains.
var hiddenIDs = map[Kind]string{
	Asset:        "SELECT asset_id FROM public.asset_domains WHERE domain_id = ANY($%[1]d::uuid[])",
	GlossaryTerm: "SELECT glossary_term_id FROM public.glossary_term_domains WHERE domain_id = ANY($%[1]d::uuid[])",
	DataProduct:  "SELECT data_product_id FROM public.data_product_domains WHERE domain_id = ANY($%[1]d::uuid[])",
}

const hiddenMRNs = "SELECT a.mrn FROM public.assets a JOIN public.asset_domains m ON m.asset_id = a.id WHERE m.domain_id = ANY($%[1]d::uuid[])"

func byID(kind Kind, column string) string {
	return column + " NOT IN (" + hiddenIDs[kind] + ")"
}

// A nullable column must stay visible when it names nothing.
func byMRN(column string) string {
	return "(" + column + " IS NULL OR " + column + " NOT IN (" + hiddenMRNs + "))"
}

func typed(typeColumn, idColumn string, kinds map[string]Kind) string {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	ors := make([]string, 0, len(names))
	for _, name := range names {
		inner := strings.Replace(hiddenIDs[kinds[name]], "SELECT ", "SELECT (", 1)
		inner = strings.Replace(inner, " FROM ", ")::text FROM ", 1)
		ors = append(ors, fmt.Sprintf("(%s = '%s' AND %s::text IN (%s))", typeColumn, name, idColumn, inner))
	}
	return "NOT (" + strings.Join(ors, " OR ") + ")"
}

func all(conds ...string) string { return strings.Join(conds, " AND ") }

// visible is, for every table that holds or refers to entities of a domain,
// the condition its visible rows meet.
var visible = map[string]string{
	"assets":                    byID(Asset, "id"),
	"glossary_terms":            byID(GlossaryTerm, "id"),
	"data_products":             byID(DataProduct, "id"),
	"asset_owners":              byID(Asset, "asset_id"),
	"asset_tags":                byID(Asset, "asset_id"),
	"asset_schedules":           byID(Asset, "asset_id"),
	"asset_subscriptions":       byID(Asset, "asset_id"),
	"asset_rule_memberships":    byID(Asset, "asset_id"),
	"run_history":               byID(Asset, "asset_id"),
	"asset_terms":               all(byID(Asset, "asset_id"), byID(GlossaryTerm, "glossary_term_id")),
	"asset_rule_terms":          byID(GlossaryTerm, "glossary_term_id"),
	"glossary_term_owners":      byID(GlossaryTerm, "glossary_term_id"),
	"data_product_memberships":  all(byID(Asset, "asset_id"), byID(DataProduct, "data_product_id")),
	"data_product_owners":       byID(DataProduct, "data_product_id"),
	"data_product_rules":        byID(DataProduct, "data_product_id"),
	"data_product_rule_targets": byID(DataProduct, "data_product_id"),
	"product_images":            byID(DataProduct, "data_product_id"),
	"lineage_edges":             all(byMRN("source_mrn"), byMRN("target_mrn"), byMRN("job_mrn")),
	"documentation":             byMRN("mrn"),
	"run_entities":              byMRN("entity_mrn"),
	"doc_pages":                 typed("entity_type", "entity_id", map[string]Kind{"asset": Asset, "data_product": DataProduct}),
	"search_index":              typed("type", "entity_id", map[string]Kind{"asset": Asset, "glossary": GlossaryTerm, "data_product": DataProduct}),
}

var (
	tableNames = func() []string {
		names := make([]string, 0, len(visible))
		for name := range visible {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}()
	tablePattern = regexp.MustCompile(`\b(` + strings.Join(tableNames, "|") + `)\b`)
	// A statement that writes, or locks what it reads, is not a read to filter.
	writePattern = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|TRUNCATE)\b`)
	readPattern  = regexp.MustCompile(`(?i)^\s*(SELECT|WITH)\b`)
)

// Resolver returns the IDs of the domains hidden from the caller of ctx.
type Resolver func(ctx context.Context) ([]string, error)

var resolver atomic.Pointer[Resolver]

// SetResolver installs the resolver; nil removes it.
func SetResolver(r Resolver) {
	if r == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&r)
}

type unrestrictedKey struct{}

// Unrestricted marks work the server does on its own behalf inside a request,
// which must see every row.
func Unrestricted(ctx context.Context) context.Context {
	return context.WithValue(ctx, unrestrictedKey{}, true)
}

// Hidden returns the IDs of the domains hidden from the caller of ctx.
func Hidden(ctx context.Context) ([]string, error) {
	r := resolver.Load()
	if r == nil || ctx.Value(unrestrictedKey{}) != nil {
		return nil, nil
	}
	return (*r)(ctx)
}

// Narrowed reports whether something is hidden from the caller, whose answers
// must then be neither served from a shared cache nor stored in one.
func Narrowed(ctx context.Context) bool {
	hidden, err := Hidden(ctx)
	return err != nil || len(hidden) > 0
}

// Cond is the condition the visible rows of kind meet, for a query built by
// hand: column holds the entity ID. It is "" when nothing is hidden, and it
// appends its parameter to params. A caller whose scope cannot be resolved
// sees nothing.
func Cond(ctx context.Context, kind Kind, column string, params *[]any) string {
	hidden, err := Hidden(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Resolving the read scope; the caller sees nothing")
		return "FALSE"
	}
	if len(hidden) == 0 {
		return ""
	}
	*params = append(*params, hidden)
	return fmt.Sprintf(byID(kind, column), len(*params))
}

// And is a condition prefixed with " AND ", for a query that has a WHERE.
func And(cond string) string {
	if cond == "" {
		return ""
	}
	return " AND " + cond
}

// Shadow makes a SELECT read the tables of visible without the rows hidden
// from the caller, whatever its shape: each one it names is shadowed by a CTE
// of the same name, which the planner inlines. The tables must be named
// without a schema. It appends its parameter to params and returns the query
// unchanged when nothing is hidden or the statement is not a plain read.
func Shadow(ctx context.Context, query string, params *[]any) string {
	if !readPattern.MatchString(query) || writePattern.MatchString(query) {
		return query
	}
	found := map[string]bool{}
	for _, name := range tablePattern.FindAllString(query, -1) {
		found[name] = true
	}
	if len(found) == 0 {
		return query
	}
	hidden, err := Hidden(ctx)
	if err == nil && len(hidden) == 0 {
		return query
	}
	n := 0
	if err != nil {
		log.Error().Err(err).Msg("Resolving the read scope; the caller sees nothing")
	} else {
		*params = append(*params, hidden)
		n = len(*params)
	}
	ctes := make([]string, 0, len(found))
	for _, name := range tableNames {
		if !found[name] {
			continue
		}
		keep := "FALSE"
		if err == nil {
			keep = fmt.Sprintf(visible[name], n)
		}
		ctes = append(ctes, fmt.Sprintf("%[1]s AS NOT MATERIALIZED (SELECT * FROM public.%[1]s WHERE %[2]s)", name, keep))
	}
	body := strings.TrimLeft(query, " \t\r\n")
	upper := strings.ToUpper(body)
	switch {
	case strings.HasPrefix(upper, "WITH RECURSIVE"):
		return "WITH RECURSIVE " + strings.Join(ctes, ", ") + "," + body[len("WITH RECURSIVE"):]
	case strings.HasPrefix(upper, "WITH"):
		return "WITH " + strings.Join(ctes, ", ") + "," + body[len("WITH"):]
	default:
		return "WITH " + strings.Join(ctes, ", ") + " " + body
	}
}

// DB is a pool whose reads go through Shadow, inside a transaction too.
// Writes reach the pool as they are.
type DB struct {
	*pgxpool.Pool
}

// Scoped wraps the pool of a store that serves callers.
func Scoped(pool *pgxpool.Pool) DB {
	return DB{Pool: pool}
}

func (d DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	sql = Shadow(ctx, sql, &args)
	return d.Pool.Query(ctx, sql, args...)
}

func (d DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	sql = Shadow(ctx, sql, &args)
	return d.Pool.QueryRow(ctx, sql, args...)
}

func (d DB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return scopedTx{Tx: tx}, nil
}

func (d DB) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := d.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return scopedTx{Tx: tx}, nil
}

type scopedTx struct {
	pgx.Tx
}

func (t scopedTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := t.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return scopedTx{Tx: tx}, nil
}

func (t scopedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	sql = Shadow(ctx, sql, &args)
	return t.Tx.Query(ctx, sql, args...)
}

func (t scopedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	sql = Shadow(ctx, sql, &args)
	return t.Tx.QueryRow(ctx, sql, args...)
}
