package metamodel

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const exampleProfile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: retention
    type: integer
    core: true
    required: true
    storage: metadata.example.retention
    validation:
      minimum: 1
    presentation:
      labelKey: example.retention.label
`

func TestProfileExtendsNativeWithoutChangingIdentity(t *testing.T) {
	r, err := Load(strings.NewReader(exampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Field("tags"); !ok {
		t.Fatal("native tags missing")
	}
	if f, ok := r.Field("name"); !ok || f.Storage != "marmot.name" || !f.Required {
		t.Fatal("native name contract changed")
	}
	if !r.Enabled() || len(r.Schema().Fields) != len(Native().Schema().Fields)+1 {
		t.Fatal("profile was not composed")
	}
	if err := r.Validate(map[string]any{"name": "table", "retention": 30.0}, "asset", true); err != nil {
		t.Fatal(err)
	}
	// A governed required field only absent is completeness, not validity: Validate lets it
	// through, Missing reports it.
	if err := r.Validate(map[string]any{"name": "table"}, "asset", true); err != nil {
		t.Fatalf("absent governed required field must not block Validate: %v", err)
	}
	if missing := r.Missing(map[string]any{"name": "table"}, "asset", true); len(missing) != 1 || missing[0] != (Violation{"retention", "required"}) {
		t.Fatalf("expected retention reported missing: %v", missing)
	}
	if missing := r.Missing(map[string]any{"name": "table", "retention": 30.0}, "asset", true); len(missing) != 0 {
		t.Fatalf("satisfied field must not be reported missing: %v", missing)
	}
	for _, values := range []map[string]any{{"name": "table", "retention": 0.0}, {"name": "table", "retention": "30"}} {
		var invalid *ValidationError
		if err := r.Validate(values, "asset", true); !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0].Field != "retention" {
			t.Fatalf("expected retention violation: %v", err)
		}
	}
	// Native structural fields keep blocking on absence: it's identity, not governance completeness.
	var invalid *ValidationError
	if err := r.Validate(map[string]any{"retention": 30.0}, "asset", true); !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0] != (Violation{"name", "required"}) {
		t.Fatalf("expected name violation: %v", err)
	}
	snapshot := r.Schema()
	snapshot.Fields[0].Required = false
	if !r.Schema().Fields[0].Required {
		t.Fatal("schema snapshot mutated registry")
	}
	r2, _ := Load(strings.NewReader(exampleProfile))
	if r.Schema().Hash != r2.Schema().Hash {
		t.Fatal("unstable schema hash")
	}
}

func TestRejectInvalidDefinitions(t *testing.T) {
	for name, document := range map[string]string{
		"unknown property":        exampleProfile + "unexpected: true\n",
		"shadow native ownership": strings.ReplaceAll(exampleProfile, "retention", "owners"),
		"shadow native identity":  strings.ReplaceAll(exampleProfile, "retention", "mrn"),
		"duplicate YAML key":      exampleProfile + "id: replacement\n",
		"unknown type":            strings.Replace(exampleProfile, "type: integer", "type: executable", 1),
		"invalid binding":         strings.Replace(exampleProfile, "metadata.example.retention", "marmot.mrn", 1),
		"prototype binding":       strings.Replace(exampleProfile, "metadata.example.retention", "metadata.constructor.retention", 1),
		"required nullable":       strings.Replace(exampleProfile, "required: true", "required: true\n    nullable: true", 1),
		"format version":          strings.Replace(exampleProfile, "formatVersion: 1", "formatVersion: 2", 1),
		"native override":         strings.ReplaceAll(strings.ReplaceAll(exampleProfile, "retention", "name"), "metadata.example.name", "marmot.name"),
		"duplicate id":            exampleProfile + "  - id: retention\n    type: string\n    storage: metadata.example.other\n    core: true\n",
		"irrelevant constraint":   strings.Replace(exampleProfile, "minimum: 1", "minLength: 1", 1),
		"unsupported reference":   strings.Replace(exampleProfile, "type: integer", "type: reference", 1),
		"nullable native tags":    "formatVersion: 1\nid: example\nversion: 1\ndefaultLocale: en\nfields:\n  - id: tags\n    type: list\n    itemType: string\n    core: true\n    nullable: true\n    storage: marmot.tags\n",
		"facet on integer field":  strings.Replace(exampleProfile, "labelKey: example.retention.label", "labelKey: example.retention.label\n      facet: true", 1),
		"term link on asset":      strings.Replace(exampleProfile, "type: integer", "type: string", 1) + "      control: glossary_term\n",
		"term link on integer":    strings.Replace(exampleProfile, "required: true", "required: true\n    appliesTo:\n      kinds: [glossary_term]", 1) + "      control: glossary_term\n",
		"inverse label alone":     exampleProfile + "      inverseLabelKey: example.retention.inverse\n",
		"search on integer":       exampleProfile + "      control: search\n",
		"badge on integer":        exampleProfile + "      badge: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(document)); err == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
	if _, err := Load(strings.NewReader(strings.Repeat("x", MaxProfileBytes+1))); err == nil {
		t.Fatal("accepted oversized profile")
	}
}

func TestOptionalCoreAndKindScoping(t *testing.T) {
	profile := strings.Replace(exampleProfile, "required: true", "required: false", 1)
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(map[string]any{"name": "table"}, "asset", true); err != nil {
		t.Fatal(err)
	}

	r, err = Load(strings.NewReader(exampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Fields("asset")) == 0 || len(r.Fields("data_product")) != 0 {
		t.Fatal("appliesTo with no kinds must default to asset only")
	}
	if err := r.Validate(map[string]any{"name": "stub"}, "asset", false); err != nil {
		t.Fatal(err)
	}
	if missing := r.Missing(map[string]any{"name": "stub"}, "asset", false); len(missing) != 0 {
		t.Fatalf("stubs are unconditionally exempt from completeness: %v", missing)
	}
	if missing := r.Missing(map[string]any{"name": "table"}, "asset", true); len(missing) != 1 || missing[0].Field != "retention" {
		t.Fatalf("expected retention reported missing: %v", missing)
	}
}

func TestAppliesToScopesFieldsByKind(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: cost_center
    type: string
    core: true
    storage: metadata.example.cost_center
    appliesTo:
      kinds: [data_product]
    presentation:
      labelKey: example.cost_center.label
  - id: shared_owner
    type: string
    core: true
    storage: metadata.example.shared_owner
    appliesTo:
      kinds: [asset, data_product]
    presentation:
      labelKey: example.shared_owner.label
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Field("cost_center"); !ok {
		t.Fatal("field IDs stay registered regardless of kind")
	}
	assetIDs := fieldIDs(r.Fields("asset"))
	if slices.Contains(assetIDs, "cost_center") || !slices.Contains(assetIDs, "shared_owner") {
		t.Fatalf("unexpected asset fields: %v", assetIDs)
	}
	productIDs := fieldIDs(r.Fields("data_product"))
	if !slices.Contains(productIDs, "cost_center") || !slices.Contains(productIDs, "shared_owner") {
		t.Fatalf("unexpected data_product fields: %v", productIDs)
	}
	if err := r.Validate(map[string]any{"cost_center": 3}, "data_product", true); err == nil {
		t.Fatal("wrong type for a data_product field must still fail validity")
	}
}

func TestMetadataBindingWithoutNamespace(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: asset_type
    type: string
    core: true
    nullable: true
    storage: metadata.asset_type
    presentation:
      labelKey: example.asset_type.label
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(map[string]any{"name": "Demo", "asset_type": "dashboard"}, "asset", true); err != nil {
		t.Fatalf("a namespace-less binding must validate a value ingestion already wrote there: %v", err)
	}
}

func fieldIDs(fields []Field) []string {
	ids := make([]string, len(fields))
	for i, f := range fields {
		ids[i] = f.ID
	}
	return ids
}

func TestSupportedValues(t *testing.T) {
	for _, tc := range []struct {
		field     Field
		good, bad any
	}{
		{Field{Type: "boolean"}, false, "false"},
		{Field{Type: "integer"}, 3.0, 3.5},
		{Field{Type: "date"}, "2026-09-17", "2026-02-30"},
		{Field{Type: "enum", Values: []string{"internal", "public"}}, "internal", "translated value"},
		{Field{Type: "list", ItemType: "integer"}, []any{1.0, 2.0}, []any{1.0, "2"}},
	} {
		t.Run(tc.field.Type, func(t *testing.T) {
			if code := validateValue(tc.field, tc.good); code != "" {
				t.Fatalf("valid value rejected: %s", code)
			}
			if code := validateValue(tc.field, tc.bad); code == "" {
				t.Fatal("invalid value accepted")
			}
		})
	}
}

func TestFacetPresentation(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: classification
    type: enum
    core: true
    storage: metadata.example.classification
    values: [public, confidential]
    presentation:
      labelKey: example.classification.label
      facet: true
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := r.Field("classification")
	if !ok || !f.Presentation.Facet {
		t.Fatal("facet flag not carried into the registered field")
	}
}

func TestMessagesCatalog(t *testing.T) {
	profile := exampleProfile + "messages:\n  en:\n    example.retention.label: Retention (days)\n  es:\n    example.retention.label: Retención (días)\n"
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	messages := r.Schema().Messages
	if messages["en"]["example.retention.label"] != "Retention (days)" || messages["es"]["example.retention.label"] != "Retención (días)" {
		t.Fatalf("messages not carried into schema: %v", messages)
	}
	r2, _ := Load(strings.NewReader(exampleProfile))
	if r.Schema().Hash == r2.Schema().Hash {
		t.Fatal("messages did not change the schema hash")
	}
}

func TestRejectInvalidMessages(t *testing.T) {
	for name, document := range map[string]string{
		"invalid locale": exampleProfile + "messages:\n  \"en us\":\n    k: v\n",
		"invalid key":    exampleProfile + "messages:\n  en:\n    \"bad key\": v\n",
		"empty value":    exampleProfile + "messages:\n  en:\n    k: \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(document)); err == nil {
				t.Fatal("accepted invalid messages")
			}
		})
	}
}

func TestGovernedRequiredNeverBlocksAbsenceOrEmpty(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: owner_note
    type: string
    core: true
    required: true
    storage: metadata.example.owner_note
    presentation:
      labelKey: example.owner_note.label
  - id: classification
    type: enum
    core: true
    required: true
    storage: metadata.example.classification
    values: [public, internal]
    presentation:
      labelKey: example.classification.label
  - id: tags_custom
    type: list
    itemType: string
    core: true
    required: true
    storage: metadata.example.tags_custom
    presentation:
      labelKey: example.tags_custom.label
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"name": "table"}
	if err := r.Validate(base, "asset", true); err != nil {
		t.Fatalf("absent governed required fields must not block: %v", err)
	}
	if err := r.Validate(mergeValues(base, map[string]any{"owner_note": "", "tags_custom": []any{}}), "asset", true); err != nil {
		t.Fatalf("empty-but-present governed required fields must not block: %v", err)
	}
	if missing := r.Missing(base, "asset", true); len(missing) != 3 {
		t.Fatalf("expected all three fields reported missing: %v", missing)
	}

	// An explicit null still fails: required implies not nullable, and that stays a validity rule.
	var invalid *ValidationError
	if err := r.Validate(mergeValues(base, map[string]any{"owner_note": nil}), "asset", true); !errors.As(err, &invalid) || invalid.Fields[0] != (Violation{"owner_note", "not_nullable"}) {
		t.Fatalf("expected not_nullable violation for explicit null: %v", err)
	}

	// A present value is still checked for validity: an undeclared enum member still fails,
	// even though the field is exempt from the required check.
	if err := r.Validate(mergeValues(base, map[string]any{"classification": "secret"}), "asset", true); !errors.As(err, &invalid) || invalid.Fields[0] != (Violation{"classification", "enum"}) {
		t.Fatalf("expected enum violation: %v", err)
	}
}

func mergeValues(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestGlossaryTermControl(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: stands_for
    type: list
    itemType: string
    core: true
    storage: metadata.example.stands_for
    appliesTo:
      kinds: [glossary_term]
    presentation:
      labelKey: example.stands_for.label
      control: glossary_term
      inverseLabelKey: example.stands_for.inverse
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := r.Field("stands_for")
	if !ok || f.Presentation.Control != ControlGlossaryTerm || f.Presentation.InverseLabelKey != "example.stands_for.inverse" {
		t.Fatalf("field = %+v", f)
	}
}

func TestValueLabelKeys(t *testing.T) {
	const head = "formatVersion: 1\nid: example\nversion: 1\ndefaultLocale: en\nfields:\n"
	enum := head + `  - id: classification
    type: enum
    core: true
    storage: metadata.example.classification
    values: [public, internal]
    presentation:
      labelKey: example.classification.label
      valueLabelKeys:
        public: example.classification.public
        internal: example.classification.internal
`
	r, err := Load(strings.NewReader(enum))
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := r.Field("classification"); f.Presentation.ValueLabelKeys["public"] != "example.classification.public" {
		t.Fatalf("keys not carried: %+v", f.Presentation)
	}
	for name, document := range map[string]string{
		"enum list": head + "  - id: channels\n    type: list\n    itemType: enum\n    core: true\n    storage: metadata.example.channels\n    values: [api, sftp]\n    presentation:\n      labelKey: example.channels.label\n      valueLabelKeys:\n        api: example.channels.api\n",
		"boolean":   head + "  - id: pii\n    type: boolean\n    core: true\n    storage: metadata.example.pii\n    presentation:\n      labelKey: example.pii.label\n      valueLabelKeys:\n        \"true\": example.pii.yes\n        \"false\": example.pii.no\n",
		"partial":   strings.Replace(enum, "        internal: example.classification.internal\n", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(document)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, document := range map[string]string{
		"unknown value":    strings.Replace(enum, "internal: example", "secret: example", 1),
		"invalid key":      strings.Replace(enum, "example.classification.public", "example classification", 1),
		"string field":     head + "  - id: note\n    type: string\n    core: true\n    storage: metadata.example.note\n    presentation:\n      labelKey: example.note.label\n      valueLabelKeys:\n        x: example.note.x\n",
		"boolean non-bool": head + "  - id: pii\n    type: boolean\n    core: true\n    storage: metadata.example.pii\n    presentation:\n      labelKey: example.pii.label\n      valueLabelKeys:\n        maybe: example.pii.maybe\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(document)); err == nil {
				t.Fatal("accepted invalid valueLabelKeys")
			}
		})
	}
}

func TestBadgeAndSearchControl(t *testing.T) {
	profile := `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: term_type
    type: enum
    core: true
    storage: metadata.example.term_type
    values: [business_term, acronym]
    appliesTo:
      kinds: [glossary_term]
    presentation:
      labelKey: example.term_type.label
      badge: true
  - id: synonyms
    type: list
    itemType: string
    core: true
    storage: metadata.synonyms
    appliesTo:
      kinds: [glossary_term]
    presentation:
      labelKey: example.synonyms.label
      control: search
`
	r, err := Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := r.Field("term_type"); !f.Presentation.Badge {
		t.Fatal("badge not carried")
	}
	if f, _ := r.Field("synonyms"); f.Presentation.Control != ControlSearch {
		t.Fatal("search control not carried")
	}
}

func TestSchemaForKindPublishesEachKindsNativeAttributes(t *testing.T) {
	r, err := Load(strings.NewReader(`formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: cost_center
    type: string
    core: true
    storage: metadata.example.cost_center
    appliesTo:
      kinds: [data_product]
    presentation:
      labelKey: example.cost_center.label
`))
	if err != nil {
		t.Fatal(err)
	}
	product := r.SchemaForKind("data_product")
	if got := fieldIDs(product.Fields); !slices.Equal(got, []string{"name", "description", "tags", "cost_center"}) {
		t.Fatalf("data_product schema = %v", got)
	}
	if !product.Fields[0].Required || product.Fields[0].Storage != "marmot.name" {
		t.Fatalf("a product's name is required and native: %+v", product.Fields[0])
	}
	if got := fieldIDs(r.SchemaForKind("glossary_term").Fields); !slices.Equal(got, []string{"name", "definition", "description", "tags"}) {
		t.Fatalf("glossary_term schema = %v", got)
	}
	if got := fieldIDs(r.SchemaForKind("asset").Fields); slices.Contains(got, "definition") || !slices.Contains(got, "name") {
		t.Fatalf("asset schema = %v", got)
	}
	// Read-only: validation keeps seeing only the profile's fields.
	if slices.Contains(fieldIDs(r.Fields("data_product")), "name") {
		t.Fatal("native product attributes must not enter Fields")
	}
	if err := r.Validate(map[string]any{}, "data_product", true); err != nil {
		t.Fatalf("a missing native name must not fail registry validation: %v", err)
	}
	if missing := r.Missing(map[string]any{}, "glossary_term", true); len(missing) != 0 {
		t.Fatalf("native term attributes must not count as missing: %v", missing)
	}
}

func TestSchemaForKindLetsTheProfileOverrideANativeID(t *testing.T) {
	r, err := Load(strings.NewReader(`formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: definition
    type: string
    core: true
    storage: metadata.example.definition
    appliesTo:
      kinds: [glossary_term]
    presentation:
      labelKey: example.definition.label
`))
	if err != nil {
		t.Fatal(err)
	}
	var storages []string
	for _, f := range r.SchemaForKind("glossary_term").Fields {
		if f.ID == "definition" {
			storages = append(storages, f.Storage)
		}
	}
	if !slices.Equal(storages, []string{"metadata.example.definition"}) {
		t.Fatalf("definition bindings = %v", storages)
	}
}
