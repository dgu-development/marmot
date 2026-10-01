---
sidebar_position: 3
title: Metadata quality
description: Audit how complete and valid the metadata of every asset is, on demand, with the criterion of the metamodel profile, and keep the history of the runs apart from the catalog.
---

# Metadata quality

The audit scores every asset in the catalog against the effective [metamodel](asset-metadata.md): how many of its fields are filled (completeness) and how many of those hold a valid value (conformity). It runs on the server, with the same field checks the API applies to writes, so a value the server would reject is a finding here.

It is off by default.

```yaml
quality:
  enabled: true
```

```
MARMOT_QUALITY_ENABLED=true
```

Without a metamodel profile there is nothing to audit against: starting a run answers `422`.

## Permissions

| Permission | Allows |
| --- | --- |
| `metadata_quality:view` | Read the settings, the runs and their results |
| `metadata_quality:run` | Start a run |
| `metadata_quality:manage` | Change the settings |

`admin` and the `metadata_quality_auditor` role hold all three; `user` holds `view`. The audit covers the whole platform: the role is not scoped to a domain.

## Settings

`GET`/`PUT /api/v1/quality/settings` read and replace one versioned row, with `If-Match`: a stale version answers `412`. They hold the weights of completeness and conformity, the thresholds of a compliant and a warned asset, which rules apply and with what severity, the retention, the batch size and the time limit of a run.

## Runs

`POST /api/v1/quality/runs` starts a run in the background and answers `202` with the run. One run at a time: a second request answers `409` with the run in progress. Read it with `GET /api/v1/quality/runs/{id}`: `processed` over `total` is the progress.

A run walks the catalog by key in batches (`batch_size`, 500 by default), so memory stays bounded to one batch. Each batch is stored in its own transaction, which means a failure leaves the earlier batches done. It ends `succeeded`, or `failed` with its reason: `timeout` when it passes `max_run_seconds`, `interrupted` when the server stops or stops reporting progress for five minutes, or the error. A crash never blocks the next run.

| Endpoint | Returns |
| --- | --- |
| `GET /api/v1/quality/runs` | Runs, newest first, with the summary of the finished ones |
| `GET /api/v1/quality/runs/{id}` | The run, the settings it used and the metamodel version and hash |
| `GET /api/v1/quality/runs/{id}/results` | One entry per asset, weakest first; filters `status`, `domain` (an id, or `unassigned`), `type`, `q`; `sort=name`; `limit` (up to 500) and `offset` |

The summary has the totals, the status counts, the fields with most findings and the mean quality by section, asset type and domain. Domains are one more aggregate of the run, not a filter of it. Stubs, the placeholders lineage creates, are counted apart and never scored.

### Rules and findings

A finding has a field, a code and the rule it belongs to. Codes of a value are the metamodel's (`type`, `length`, `enum`, `date`, `range`, `items`) plus `required`; the coherence rules add `pii_coherence` (an asset that contains personal data needs a classification and a steward), `review_expired` and `external_link_empty`. A disabled rule raises nothing. The fields the audit itself writes (`quality_score` and its companions) are not part of the calculation.

The detail of the findings is kept for the last successful run only; every result carries its `issue_count`.

## Where it is stored

Runs, results and findings have their own tables. Nothing of the audit is a catalog asset, so it does not appear in Discover, in search, in the facets or in the asset counts, and it does not need a plugin or a provider.

| Data | Kept |
| --- | --- |
| Run and its summary | `retention.run_days` (730) |
| Result of each asset | `retention.result_days` (90) |
| Findings | Last successful run |

The oldest are pruned when a run finishes. A value of `0` keeps nothing beyond the latest run, which is never left empty. The result of an asset outlives the asset.

## Schedule

`schedule` in the settings is a five-field cron expression; empty means runs are manual only. It is read as the ingestion schedules read theirs, so the same expression means the same hour in a pipeline and in the audit: the server's time zone, unless the expression names its own (`CRON_TZ=Europe/Madrid 0 3 * * *`).

A check every 30 seconds, on a singleton task like the ingestion scheduler's, starts a run when the next slot after the later of the last run and the last change of the settings has passed:

- A run that is already going, or a manual one that started after the slot, means the slot is met: it never starts two runs for one slot.
- A slot missed while the server was down fires once, not once per missed slot.
- A schedule that has just been saved waits for its next slot; it does not fire for one that passed before it existed.
- With several replicas only one checks at a time, and the single-run guard backs it up.

`GET`/`PUT /api/v1/quality/settings` add `next_run` when a schedule is set. Runs started by it have `trigger` `schedule` and no `triggered_by`.

## Scores on the assets

Each run writes the score it computed on the assets, as the platform: `quality_score` (a fraction between 0 and 1 with three decimals), `quality_dimensions` (the checks the asset meets: description, tags, ownership, classification, review, documentation, resource, completeness and conformity) and `quality_evaluated_at` (the day it was judged). Each is written only when the profile has the field, so a profile without them audits and writes nothing.

- Only what changed is written. An asset whose score and checks already say what the audit found is left alone, and the date alone never forces a write, so its version does not move run after run. `updated_at` and `version` do advance when a score changes: it is a real write.
- The write is made against the version the audit read. An asset edited in between is skipped (`score_conflicts` in the run) and scored by the next run.
- It does not notify whoever follows the asset, and it is not judged against the rest of the asset: an asset with an invalid value elsewhere is exactly the one the audit has to be able to score.
- `documentation` means there is something to read in the first tab of the asset: a page written in the platform, the older per-source documentation, or a markdown body that a source ingested (`metadata.dgu.body`). `resource` means the asset links out to a resource, an external link with a URL. They are separate checks; a check the profile's `quality_dimensions` does not list yet is left out of the write instead of failing it.
- Placeholders created by lineage (stubs) are never written.
- `scores_written`, `score_conflicts` and `score_failures` in the run say how it went.

### System fields

A field marked `system: true` in the metamodel profile is written only by the platform. It must be bound to `metadata.*`, apply to assets only and be neither required, derived nor defaulted. The API refuses a write to it with the code `system` (a `PATCH`, like a derived field's `derived`); a whole-asset update, a creation or a discovery run leaves it as it was whatever they send, so a client that sends back the asset it read changes nothing. The interface shows it read-only. Mark `quality_score`, `quality_dimensions` and `quality_evaluated_at` with it in the profile; a server that does not know the key refuses the profile, so the profile and the server version go together.

## Not included yet

Rules declared in the profile (the coherence rules are fixed in the server today) are the next delivery. Reads are open to anyone with `metadata_quality:view`: when domains whose content only their members can read exist, the results will follow the same restriction as search.
