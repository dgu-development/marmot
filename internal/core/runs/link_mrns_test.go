package runs

import (
	"context"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/lineage"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
	"github.com/stretchr/testify/require"
)

const linkProfile = `formatVersion: 1
id: example
version: 1
defaultLocale: en
fields:
  - id: parent_area
    type: list
    itemType: string
    core: true
    nullable: true
    storage: metadata.example.parent_area
    presentation:
      labelKey: example.parent_area.label
      control: asset
`

func TestProcessEntities_LinksByMRNAnAssetCreatedLaterInTheSameRun(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	registry, err := metamodel.Load(strings.NewReader(linkProfile))
	require.NoError(t, err)
	assets := asset.NewService(asset.NewPostgresRepository(pool, recorderStub{}), asset.WithMetamodel(registry))
	service := NewService(NewPostgresRepository(pool), assets, lineage.NewService(lineage.NewPostgresRepository(pool), assets), nil, recorderStub{})

	child := tableInput("ventas")
	child.Metadata = map[string]interface{}{
		"example": map[string]interface{}{"parent_area": []interface{}{"mrn://table/postgresql/corporacion", "mrn://table/postgresql/nowhere"}},
	}
	// The child comes first: its parent does not exist when it is created.
	process(t, service, "areas", []CreateAssetInput{child, tableInput("corporacion")}, nil)

	parent, err := assets.GetByMRN(ctx, "mrn://table/postgresql/corporacion")
	require.NoError(t, err)
	stored, err := assets.GetByMRN(ctx, "mrn://table/postgresql/ventas")
	require.NoError(t, err)
	value, _ := metamodel.ValueAt(stored.Metadata, "metadata.example.parent_area")
	require.Equal(t, []string{parent.ID}, asset.LinkIDs(value), "the MRN becomes the parent's ID; one that names nothing is dropped")
}
