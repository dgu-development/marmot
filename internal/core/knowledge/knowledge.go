// Package knowledge compiles reviewed catalog knowledge without feeding generated pages back into sources.
package knowledge

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("knowledge entity not found")
	ErrInvalid  = errors.New("invalid knowledge input")
	ErrConflict = errors.New("knowledge draft or sources changed; compile and review again")
	ErrTooLarge = errors.New("knowledge sources exceed compilation limit")
)

type Entity struct {
	Kind string `json:"entity_type"`
	ID   string `json:"entity_id"`
}

type Source struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Hash    string `json:"hash"`
	Field   string `json:"field"`
	Preview string `json:"preview,omitempty"`
	Text    string `json:"-"`
}

type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type Page struct {
	Documents         []Document `json:"documents,omitempty"`
	DraftCompilerHash string     `json:"-"`
	Error             string     `json:"error,omitempty"`
	Entity
	Title             string     `json:"title"`
	EntityURL         string     `json:"entity_url"`
	Description       string     `json:"description"`
	DocEntityID       string     `json:"doc_entity_id"`
	Content           string     `json:"content"`
	DraftContent      string     `json:"draft_content"`
	DraftHash         string     `json:"draft_hash"`
	SourceHash        string     `json:"source_hash"`
	DraftSourceHash   string     `json:"draft_source_hash"`
	CurrentSourceHash string     `json:"current_source_hash"`
	Freshness         string     `json:"freshness"`
	DraftFreshness    string     `json:"draft_freshness"`
	Status            string     `json:"status"`
	Sources           []Source   `json:"sources"`
	PublishedSources  []Source   `json:"published_sources"`
	DraftSources      []Source   `json:"draft_sources"`
	CompiledAt        *time.Time `json:"compiled_at"`
	PublishedAt       *time.Time `json:"published_at"`
	Mode              string     `json:"mode"`
	DraftMode         string     `json:"draft_mode"`
}

type ListResult struct {
	Pages  []Page `json:"pages"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type BatchResult struct {
	Compiled   int          `json:"compiled"`
	Skipped    int          `json:"skipped"`
	Errors     []BatchError `json:"errors"`
	NextOffset int          `json:"next_offset"`
	Total      int          `json:"total"`
}

type BatchError struct {
	Entity
	Error string `json:"error"`
}

type Options struct {
	Endpoint       string
	APIKey         string
	Model          string
	Schema         json.RawMessage
	DomainsEnabled bool
}

type Service struct {
	db     *pgxpool.Pool
	opts   Options
	client *http.Client
}

func NewService(db *pgxpool.Pool, opts Options) *Service {
	return &Service{db: db, opts: opts, client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
