package extension

import (
	"context"
	"errors"
	"time"
)

// Errors of the host's services. The host wraps its own with these, so an
// extension tells them apart with errors.Is.
var (
	ErrNotFound        = errors.New("not found")
	ErrForbidden       = errors.New("forbidden")
	ErrVersionConflict = errors.New("version conflict")
)

// FieldsError is a write the metadata profile refused.
type FieldsError struct {
	Fields []FieldViolation
}

func (e *FieldsError) Error() string { return "the profile refused the field values" }

// FieldViolation names a field and the stable code of what is wrong with it.
type FieldViolation struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// User is a person of the catalog.
type User struct {
	ID       string
	Username string
	Name     string
	Active   bool
}

// Users looks people up. A missing one is ErrNotFound.
type Users interface {
	Get(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
}

type Team struct {
	ID   string
	Name string
}

type TeamMember struct {
	UserID string
}

// Teams looks teams up. A missing one is ErrNotFound.
type Teams interface {
	GetTeam(ctx context.Context, id string) (*Team, error)
	GetTeamByName(ctx context.Context, name string) (*Team, error)
	ListMembers(ctx context.Context, teamID string) ([]*TeamMember, error)
}

// Subject types of a RoleAssignment.
const (
	SubjectUser = "user"
	SubjectTeam = "team"
)

// RoleAssignment is who holds a role in a domain, inherited ones included.
type RoleAssignment struct {
	Role        string
	SubjectType string
	SubjectID   string
	// SubjectMissing marks a row whose user or team was deleted.
	SubjectMissing bool
}

// Asset is what an extension reads of an asset. Version is what PatchFields
// takes to detect a concurrent change.
type Asset struct {
	ID      string
	MRN     string
	Name    string
	Type    string
	Version int64
	Tags    []string
}

// Assets reads and writes assets as the caller: writes go through the same
// guards as the API, with the person the context carries (see Host.As). A
// missing asset is ErrNotFound, a stale version ErrVersionConflict, a write
// the domain does not allow ErrForbidden and one the profile refuses a
// *FieldsError.
type Assets interface {
	Get(ctx context.Context, id string) (*Asset, error)
	// PatchFields writes profile fields by ID; nil clears one.
	PatchFields(ctx context.Context, id string, version int64, fields map[string]any) (*Asset, error)
	AddTag(ctx context.Context, id, tag string) (*Asset, error)
	RemoveTag(ctx context.Context, id, tag string) (*Asset, error)
	AddTerms(ctx context.Context, assetID string, termIDs []string, source, createdBy string) error
	RemoveTerm(ctx context.Context, assetID, termID string) error
	// Coerce turns text into what the asset field with that ID holds (a
	// number, a boolean, a list). known is false for a field the profile does
	// not have, and the value comes back as it was.
	Coerce(fieldID string, value any) (coerced any, known bool, err error)
}

type Term struct {
	ID   string
	Name string
}

// Glossary looks terms up. A missing one is ErrNotFound.
type Glossary interface {
	GetByName(ctx context.Context, name string) (*Term, error)
}

// Queries resolves a Discover query to the assets it matches.
type Queries interface {
	Match(ctx context.Context, query string, limit int) (ids []string, total int, err error)
}

// RecipientUser is the Recipient type of a person.
const RecipientUser = "user"

type Recipient struct {
	Type string
	ID   string
}

// Notification is an in-app notification; the host delivers it to each
// recipient through the channels they chose.
type Notification struct {
	Recipients []Recipient
	Type       string
	Title      string
	Message    string
	Data       map[string]any
}

type Notifier interface {
	Create(ctx context.Context, n Notification) error
}

// Task is work the host repeats, on one replica at a time.
type Task struct {
	// Name identifies it across replicas; prefix it with the extension's ID.
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context) error
}
