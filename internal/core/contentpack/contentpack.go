// Package contentpack creates the reference assets a deployment ships with: a regulation and its
// controls, the roles of a governance framework. A pack is trusted configuration, like the
// metamodel profile, and is applied at start-up through the asset service, so the profile
// validates what it creates.
package contentpack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"sigs.k8s.io/yaml"
)

const (
	maxPackBytes = 1 << 20
	createdBy    = "system"
)

// Pack is one YAML file of the packs directory.
type Pack struct {
	FormatVersion int     `json:"formatVersion"`
	ID            string  `json:"id"`
	Assets        []Asset `json:"assets"`
}

// Asset is an asset to create when no asset has its MRN. Fields are profile fields by ID; a field
// with the asset control takes the MRNs of the assets it points to, which must exist by then: an
// earlier asset of the same pack, or one of a pack that sorts before it.
type Asset struct {
	MRN         string         `json:"mrn"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Providers   []string       `json:"providers"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// Assets is what applying a pack needs of the asset service.
type Assets interface {
	GetByMRN(ctx context.Context, mrn string) (*asset.Asset, error)
	Create(ctx context.Context, input asset.CreateInput) (*asset.Asset, error)
}

// LoadDir reads the *.yaml packs of dir in name order. An empty dir means no packs.
func LoadDir(dir string) ([]Pack, error) {
	if dir == "" {
		return nil, nil
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	packs := make([]Pack, 0, len(paths))
	for _, path := range paths {
		pack, err := loadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		packs = append(packs, pack)
	}
	return packs, nil
}

func loadFile(path string) (Pack, error) {
	var pack Pack
	info, err := os.Stat(path)
	if err != nil {
		return pack, err
	}
	if info.Size() > maxPackBytes {
		return pack, errors.New("pack exceeds 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pack, err
	}
	data, err = yaml.YAMLToJSONStrict(data)
	if err != nil {
		return pack, fmt.Errorf("decoding YAML: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pack); err != nil {
		return pack, fmt.Errorf("decoding pack: %w", err)
	}
	if pack.FormatVersion != 1 {
		return pack, fmt.Errorf("unsupported formatVersion %d", pack.FormatVersion)
	}
	if pack.ID == "" {
		return pack, errors.New("id is required")
	}
	for i, a := range pack.Assets {
		if !strings.HasPrefix(a.MRN, "mrn://") || a.Name == "" || a.Type == "" || len(a.Providers) == 0 {
			return pack, fmt.Errorf("assets[%d]: mrn, name, type and providers are required", i)
		}
	}
	return pack, nil
}

// Apply creates the assets of the packs that do not exist yet and returns how many it created.
// An asset that exists is left as it is, whatever the pack says: people may have edited it. One
// asset failing does not stop the rest; the error names each one.
func Apply(ctx context.Context, packs []Pack, registry *metamodel.Registry, assets Assets) (int, error) {
	var (
		created int
		errs    []error
	)
	for _, pack := range packs {
		for _, a := range pack.Assets {
			ok, err := create(ctx, a, registry, assets)
			if err != nil {
				errs = append(errs, fmt.Errorf("pack %s, %s: %w", pack.ID, a.MRN, err))
			}
			if ok {
				created++
			}
		}
	}
	return created, errors.Join(errs...)
}

func create(ctx context.Context, a Asset, registry *metamodel.Registry, assets Assets) (bool, error) {
	if _, err := assets.GetByMRN(ctx, a.MRN); err == nil {
		return false, nil
	} else if !errors.Is(err, asset.ErrAssetNotFound) {
		return false, err
	}
	metadata := map[string]any{}
	for id, value := range a.Fields {
		field, ok := registry.Field(id)
		if !ok || !strings.HasPrefix(field.Storage, "metadata.") {
			return false, fmt.Errorf("field %q is not a metadata field of the profile", id)
		}
		if field.Presentation.Control == metamodel.ControlAsset {
			var err error
			if value, err = linkIDs(ctx, value, assets); err != nil {
				return false, fmt.Errorf("field %q: %w", id, err)
			}
		}
		if err := metamodel.SetValueAt(metadata, field.Storage, value); err != nil {
			return false, fmt.Errorf("field %q: %w", id, err)
		}
	}
	input := asset.CreateInput{
		Name:      &a.Name,
		MRN:       &a.MRN,
		Type:      a.Type,
		Providers: a.Providers,
		Metadata:  metadata,
		Tags:      a.Tags,
		CreatedBy: createdBy,
	}
	if a.Description != "" {
		input.Description = &a.Description
	}
	_, err := assets.Create(ctx, input)
	// Another replica starting at the same time may have created it first.
	if errors.Is(err, asset.ErrAlreadyExists) {
		return false, nil
	}
	return err == nil, err
}

// linkIDs turns the MRNs of a link field into the IDs the profile stores, keeping its shape.
func linkIDs(ctx context.Context, value any, assets Assets) (any, error) {
	resolve := func(item any) (string, error) {
		mrn, ok := item.(string)
		if !ok {
			return "", errors.New("a link is the MRN of an asset")
		}
		target, err := assets.GetByMRN(ctx, mrn)
		if err != nil {
			return "", fmt.Errorf("%s: %w", mrn, err)
		}
		return target.ID, nil
	}
	list, ok := value.([]any)
	if !ok {
		return resolve(value)
	}
	ids := make([]any, 0, len(list))
	for _, item := range list {
		id, err := resolve(item)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
