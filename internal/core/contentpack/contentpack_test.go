package contentpack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

const profile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - {id: reference, type: string, core: true, nullable: true, storage: metadata.x.reference, presentation: {labelKey: x.reference}}
  - id: source
    type: list
    itemType: string
    core: true
    nullable: true
    storage: metadata.x.source
    presentation: {labelKey: x.source, control: asset, inverseLabelKey: x.source.inverse}
`

const pack = `formatVersion: 1
id: law
assets:
  - mrn: mrn://regulation/acme/law
    name: The law
    type: Regulation
    providers: [Acme]
  - mrn: mrn://control/acme/law.art-1
    name: Article 1
    type: Control
    providers: [Acme]
    description: Keep a record.
    fields:
      reference: Art. 1
      source: [mrn://regulation/acme/law]
`

type fakeAssets struct {
	byMRN   map[string]*asset.Asset
	created []asset.CreateInput
}

func (f *fakeAssets) GetByMRN(_ context.Context, mrn string) (*asset.Asset, error) {
	if a, ok := f.byMRN[mrn]; ok {
		return a, nil
	}
	return nil, asset.ErrAssetNotFound
}

func (f *fakeAssets) Create(_ context.Context, input asset.CreateInput) (*asset.Asset, error) {
	a := &asset.Asset{ID: "id-" + *input.Name, MRN: input.MRN, Metadata: input.Metadata}
	f.byMRN[*input.MRN] = a
	f.created = append(f.created, input)
	return a, nil
}

func load(t *testing.T, files map[string]string) []Pack {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	packs, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return packs
}

func registry(t *testing.T) *metamodel.Registry {
	t.Helper()
	r, err := metamodel.Load(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyCreatesMissingAssetsAndResolvesLinks(t *testing.T) {
	assets := &fakeAssets{byMRN: map[string]*asset.Asset{}}
	packs := load(t, map[string]string{"law.yaml": pack})

	created, err := Apply(context.Background(), packs, registry(t), assets)
	if err != nil || created != 2 {
		t.Fatalf("created %d, err %v", created, err)
	}
	control := assets.created[1]
	if control.CreatedBy != "system" || *control.Description != "Keep a record." {
		t.Fatalf("unexpected input: %+v", control)
	}
	x := control.Metadata["x"].(map[string]any)
	if x["reference"] != "Art. 1" || x["source"].([]any)[0] != "id-The law" {
		t.Fatalf("unexpected metadata: %v", x)
	}

	created, err = Apply(context.Background(), packs, registry(t), assets)
	if err != nil || created != 0 || len(assets.created) != 2 {
		t.Fatalf("a second run created %d, err %v", created, err)
	}
}

func TestApplyReportsWhatItCannotCreateAndGoesOn(t *testing.T) {
	bad := strings.Replace(pack, "mrn://regulation/acme/law]", "mrn://regulation/acme/missing]", 1)
	bad += `  - mrn: mrn://control/acme/law.art-2
    name: Article 2
    type: Control
    providers: [Acme]
    fields: {unknown: x}
`
	assets := &fakeAssets{byMRN: map[string]*asset.Asset{}}
	created, err := Apply(context.Background(), load(t, map[string]string{"law.yaml": bad}), registry(t), assets)
	if created != 1 || err == nil {
		t.Fatalf("created %d, err %v", created, err)
	}
	for _, want := range []string{"law.art-1", "mrn://regulation/acme/missing", "law.art-2", `"unknown"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestLoadDirRejectsAPackItDoesNotUnderstand(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":  pack + "extra: 1\n",
		"format":       strings.Replace(pack, "formatVersion: 1", "formatVersion: 2", 1),
		"missing name": strings.Replace(pack, "    name: The law\n", "", 1),
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "p.yaml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDir(dir); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if packs, err := LoadDir(""); err != nil || packs != nil {
		t.Fatalf("no directory: %v, %v", packs, err)
	}
}
