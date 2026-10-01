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

## Not included yet

A schedule (the `schedule` setting is validated but nothing starts a run from it yet), writing `quality_score` into the assets, and rules declared in the profile are the next deliveries. Reads are open to anyone with `metadata_quality:view`: when domains whose content only their members can read exist, the results will follow the same restriction as search.
