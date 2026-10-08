# Content packs

Fork-only. A content pack is a YAML file of reference assets a deployment ships with: a regulation and its controls, the roles of a governance framework. The server creates the ones that are missing when it starts, through the asset service, so the [metamodel profile](web/docs/docs/Configure/asset-metadata.md) validates them like any other asset.

## Configuration

| Option | Description | Default | Environment variable |
| --- | --- | --- | --- |
| `metamodel.packs` | Directory of `*.yaml` packs, read in name order | - (no packs) | `MARMOT_METAMODEL_PACKS` |

A pack is trusted configuration, like the profile: it lives with the deployment, not in the catalog.

## Format

```yaml
formatVersion: 1
id: ai-act
assets:
  - mrn: mrn://regulation/acme/ai-act
    name: Regulation (EU) 2024/1689
    type: Regulation
    providers: [Acme]
    description: Harmonised rules on artificial intelligence.
    tags: [ai-act]
    fields:
      classification: public
  - mrn: mrn://control/acme/ai-act.art-14
    name: Art. 14 · Human oversight
    type: Control
    providers: [Acme]
    fields:
      control_reference: Art. 14
      control_source: [mrn://regulation/acme/ai-act]
```

- `mrn`, `name`, `type` and `providers` are required. Unknown keys are refused.
- `fields` holds profile fields by ID, bound to `metadata.*`. A pack does not write derived or system fields.
- A field with the `asset` control takes the MRNs of its targets, and the server stores their IDs. A target must exist by then: an earlier asset of the same pack, or one of a pack that sorts before it.

## What applying does

- An asset whose MRN exists is left as it is, whatever the pack says: people may have edited it. Changing a pack does not update the assets it already created, and removing an asset from a pack does not delete it.
- An asset the profile refuses, or whose link has no target, is logged and skipped; the rest are created and the server starts.
- A directory that cannot be read, or a pack the server does not understand, is logged and no pack is applied.
- Assets are created by `system`, outside the domain guard: packs are the deployment's own configuration.
