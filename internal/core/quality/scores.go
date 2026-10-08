package quality

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/rs/zerolog/log"
)

// The fields the audit writes on an asset, by their id in the metamodel profile. Each is written
// only when the profile has it, so a profile without them audits and writes nothing.
const (
	fieldScore      = "metadata_quality_score"
	fieldDimensions = "metadata_quality_dimensions"
	fieldEvaluated  = "metadata_quality_evaluated_at"
)

// ScoreValue is a quality out of 100 as the profile's field holds it: a fraction with three decimals.
func ScoreValue(quality float64) float64 {
	return math.Round(math.Min(100, math.Max(0, quality))*10) / 1000
}

// ScoreChanges is what to write on an asset so it says what the audit found, or nothing when it
// already does. The date alone never forces a write, so an unchanged asset is left untouched and
// its version does not move run after run.
func (a *Auditor) ScoreChanges(as *asset.Asset, result AssetResult) map[string]any {
	if result.Stub {
		return nil
	}
	changes := map[string]any{}
	if field, ok := a.byID[fieldScore]; ok {
		want := ScoreValue(result.Quality)
		stored, present := metamodel.ValueAt(as.Metadata, field.Storage)
		if number, isNumber := stored.(float64); !present || !isNumber || math.Abs(number-want) >= 0.0005 {
			changes[fieldScore] = want
		}
	}
	if field, ok := a.byID[fieldDimensions]; ok {
		stored, _ := metamodel.ValueAt(as.Metadata, field.Storage)
		// A profile that does not list a check yet (an older one) cannot hold it: leave it out
		// instead of failing the whole write.
		dimensions := knownValues(field.Values, result.Dimensions)
		if !sameList(stored, dimensions) {
			changes[fieldDimensions] = dimensions
		}
	}
	if _, ok := a.byID[fieldEvaluated]; ok && len(changes) > 0 {
		changes[fieldEvaluated] = a.today
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

func knownValues(allowed, values []string) []string {
	if len(allowed) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if slices.Contains(allowed, value) {
			out = append(out, value)
		}
	}
	return out
}

func sameList(stored any, want []string) bool {
	switch list := stored.(type) {
	case []string:
		return reflect.DeepEqual(append([]string{}, list...), append([]string{}, want...))
	case []any:
		if len(list) != len(want) {
			return false
		}
		for i, item := range list {
			if text, ok := item.(string); !ok || text != want[i] {
				return false
			}
		}
		return true
	}
	return len(want) == 0 && stored == nil
}

// ScoreWriter writes fields on an asset; the asset service is one.
type ScoreWriter interface {
	Update(ctx context.Context, id string, input asset.UpdateInput) (*asset.Asset, error)
}

// ScoreCounts is how a batch of writes went. A conflict is an asset someone edited after the audit
// read it: the next run scores it, so it is not an error.
type ScoreCounts struct{ Written, Conflicts, Failed int }

func (c *ScoreCounts) add(other ScoreCounts) {
	c.Written += other.Written
	c.Conflicts += other.Conflicts
	c.Failed += other.Failed
}

// publish writes the scores that changed as the platform: it keeps the asset's version honest
// (the write is refused if the asset moved on), does not notify whoever follows the asset and is
// not judged against the rest of the asset.
func publish(ctx context.Context, writer ScoreWriter, auditor *Auditor, assets []*asset.Asset, results []AssetResult) (ScoreCounts, error) {
	var counts ScoreCounts
	for i, as := range assets {
		changes := auditor.ScoreChanges(as, results[i])
		if changes == nil {
			continue
		}
		version := as.Version
		_, err := writer.Update(ctx, as.ID, asset.UpdateInput{
			ExpectedVersion:  &version,
			GovernedFields:   changes,
			SystemWrite:      true,
			SkipNotification: true,
		})
		switch {
		case err == nil:
			counts.Written++
		case errors.Is(err, asset.ErrVersionConflict):
			counts.Conflicts++
		case ctx.Err() != nil:
			return counts, ctx.Err()
		default:
			counts.Failed++
			log.Warn().Err(err).Str("asset", as.ID).Msg("Writing the quality score failed")
		}
	}
	return counts, nil
}
