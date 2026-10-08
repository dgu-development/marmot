---
sidebar_position: 3
title: Metadata quality
description: Audit how complete and valid the metadata of every asset is, on demand, with the criterion of the metamodel profile, and keep the history of the runs apart from the catalog.
---

# Metadata quality

The audit scores every asset in the catalog against the effective [metamodel](asset-metadata.md): how many of its fields are filled and how many of those hold a valid value, plus the rules that check its values against each other and in time. Each of those is a [quality dimension](#quality-dimensions). It runs on the server, with the same field checks the API applies to writes, so a value the server would reject is a finding here.

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

`GET`/`PUT /api/v1/quality/settings` read and replace one versioned row, with `If-Match`: a stale version answers `412`. They hold the weight of each quality dimension, the thresholds of a compliant and a warned asset, which rules apply and with what severity, the retention, the batch size and the time limit of a run.

## Runs

`POST /api/v1/quality/runs` starts a run in the background and answers `202` with the run. One run at a time: a second request answers `409` with the run in progress. Read it with `GET /api/v1/quality/runs/{id}`: `processed` over `total` is the progress.

A run walks the catalog by key in batches (`batch_size`, 500 by default), so memory stays bounded to one batch. Each batch is stored in its own transaction, which means a failure leaves the earlier batches done. It ends `succeeded`, or `failed` with its reason: `timeout` when it passes `max_run_seconds`, `interrupted` when the server stops or stops reporting progress for five minutes, or the error. A crash never blocks the next run.

| Endpoint | Returns |
| --- | --- |
| `GET /api/v1/quality/runs` | Runs, newest first, with the summary of the finished ones |
| `GET /api/v1/quality/runs/{id}` | The run, the settings it used and the metamodel version and hash |
| `GET /api/v1/quality/runs/{id}/results` | One entry per asset, weakest first; filters `status`, `domain` (an id, or `unassigned`), `type`, `q`; `sort=name`; `limit` (up to 500) and `offset` |

The summary has the totals, the status counts, the fields with most findings and the mean score of each quality dimension and the mean quality by section, asset type and domain. Domains are one more aggregate of the run, not a filter of it. Stubs, the placeholders lineage creates, are counted apart and never scored.

### Quality dimensions

Every check of the audit counts under one dimension, and an asset has a score, out of 100, for each dimension that applies to it: the checks it passed over the checks judged.

| Dimension | What it measures | Counts |
| --- | --- | --- |
| `completeness` | The values that should be there are | Fields filled over fields in scope; the checks of its rules |
| `validity` | The values there are are well formed and allowed | Fields valid over fields filled; the checks of its rules |
| `consistency` | The values agree with each other | The checks of its rules |
| `timeliness` | The values are current | The checks of its rules |

Completeness and validity always apply. Consistency and timeliness exist only through rules, so an asset has them when a rule of that dimension applies to it (its `when` holds and a check judges something) and has no score for them otherwise. The overall quality is the mean of the scores of the dimensions that apply, weighted by `weights` in the settings (30, 30, 20 and 20 by default); a dimension that does not apply leaves its weight to the others in proportion, so an asset is never penalised for a dimension nothing judged. Changing the weights, or the dimensions that apply, changes the scores: a run is only comparable with the runs made with the same weights and rules.

Uniqueness and accuracy are not dimensions here. The first compares assets with each other and the second needs a source of truth outside the catalog, and a rule judges one asset on its metadata.

The `dimensions` of an evaluated asset, and the `metadata_quality_dimensions` field, are something else: the checks of the card of the asset (description, tags, ownership...). Their scores by dimension are `scores`.

### Rules and findings

A finding has a field, a code and the rule it belongs to. A rule belongs to a dimension (the built-in `required` and `externalLinkEmpty` to completeness, `validation` and `externalLinkInvalid` to validity). The audit applies three kinds of rule:

| Source | What it judges | Who changes it |
| --- | --- | --- |
| Built-in | `required` (a required field with no value), `validation` (the metamodel's value checks: `type`, `length`, `enum`, `date`, `range`, `items`) and `externalLinkInvalid` / `externalLinkEmpty` (the structure of the external links, which are not a profile field) | Severity and on/off, in the settings |
| Profile | The `qualityRules` the metamodel profile declares | Their text and conditions are code of the distribution, read-only here; severity and on/off, in the settings |
| Custom | Rules people write in the interface | Whoever has `metadata_quality:manage`; kept in the database |

A rule never runs code: a rule is a closed set of conditions over the fields of the profile.

#### Declaring a rule

A rule applies to an asset when every `when` condition holds (always, if there are none) and then raises one finding, on its own field, for each `check` that does not.

```yaml
qualityRules:
  - id: piiCoherence
    code: pii_coherence          # the finding code clients translate; defaults to the id in snake_case
    labelKey: metamodel.rule.pii
    dimension: consistency       # completeness, validity, consistency or timeliness
    severity: warning            # what a finding counts as by default: error or warning
    when:
      - { field: contains_pii, op: equals, value: true }
    checks:
      - { field: classification, op: set }
      - { field: data_steward, op: set }
  - id: reviewExpired
    code: review_expired
    labelKey: metamodel.rule.review
    dimension: timeliness
    severity: warning
    checks:
      - { field: next_review, op: notBefore, value: today, optional: true }
```

A condition is `field`, `op` and, for most operators, `value`:

| `op` | `value` | True when |
| --- | --- | --- |
| `set` / `unset` | none | The field has / has no value (blank text and empty lists are no value) |
| `equals` / `notEquals` | A string, number or boolean | The field equals / differs from it; `notEquals` is also true when unset |
| `oneOf` / `noneOf` | A list of strings or numbers | The field is / is not one of them; `noneOf` is also true when unset |
| `contains` / `notContains` | An item | A list field has / lacks the item; `notContains` is also true when unset |
| `matches` | A regular expression ([RE2](https://github.com/google/re2/wiki/Syntax), up to 200 characters) | The whole text satisfies it |
| `minItems` / `maxItems` | A number | The list has at least / at most that many items |
| `atLeast` / `atMost` | A number | A number field is at least / at most that |
| `notBefore` / `notAfter` | `today` or `YYYY-MM-DD` | A date field is not before / after it, moved by `days` (`value: today, days: 30` is 30 days from now) |

Apart from the operators about absence, a condition on a field with no value is false. On a `check`, `optional: true` makes an empty field pass instead: there is nothing to judge, as with an optional review date. `optional` is not allowed in `when`. A rule names asset fields, native ones (`name`, `description`, `tags`) included; a profile with a rule that names a field it does not have, an unknown operator or key, a value of the wrong type or a reserved id does not load.

A finding of a declared rule counts for the status of the asset (an `error` keeps it from being compliant), and each check it judges counts in the score of its dimension. It does not change the checks of the card the asset meets.

#### Custom rules

`GET /api/v1/quality/rules` lists every rule with its source, severity and whether it applies. `POST /api/v1/quality/rules` creates a custom rule, `PUT /api/v1/quality/rules/{id}` replaces it (with `If-Match`, like the settings; `412` when someone changed it first) and `DELETE` removes it. The body is the same declaration as above with literal `name` and `description` instead of message keys, plus `enabled`, `dimension` and `severity`:

```json
{
  "name": "Tables with personal data need a steward",
  "dimension": "consistency",
  "severity": "warning",
  "enabled": true,
  "when": [{ "field": "contains_pii", "op": "equals", "value": true }],
  "checks": [{ "field": "data_steward", "op": "set" }]
}
```

A `400` lists every problem with its path (`checks[1].value`: `type_mismatch`). The id is generated (`rule_ab12cd34`) unless given; it cannot be a built-in or profile id. There are at most 100 custom rules, with at most 20 conditions each. A custom rule that names a field the profile has since lost is skipped and listed with `problem: invalid`. A run keeps the custom rules it applied (`custom_rules` when a run is read), so changing a rule afterwards does not rewrite history.

The profile and the custom rules are two sources on purpose: the profile ships with the platform and is mounted read-only, so the application never writes it; what people write lives in the database, with its audit trail.

#### Evaluating assets now

`POST /api/v1/quality/evaluate` with `{"asset_ids": [...]}` (up to 200) judges the assets as they are, with the current settings and rules, and records nothing: it is what the quality card of an asset or a data product shows. It returns the findings and the checks each asset meets; an id that is not an asset is left out.

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

Each run writes the score it computed on the assets, as the platform: `metadata_quality_score` (a fraction between 0 and 1 with three decimals), `metadata_quality_dimensions` (the checks the asset meets: description, tags, ownership, classification, review, documentation, resource, completeness and conformity) and `metadata_quality_evaluated_at` (the day it was judged). Each is written only when the profile has the field, so a profile without them audits and writes nothing.

- Only what changed is written. An asset whose score and checks already say what the audit found is left alone, and the date alone never forces a write, so its version does not move run after run. `updated_at` and `version` do advance when a score changes: it is a real write.
- The write is made against the version the audit read. An asset edited in between is skipped (`score_conflicts` in the run) and scored by the next run.
- It does not notify whoever follows the asset, and it is not judged against the rest of the asset: an asset with an invalid value elsewhere is exactly the one the audit has to be able to score.
- `documentation` means there is something to read in the first tab of the asset: a page written in the platform, the older per-source documentation, or a markdown body that a source ingested (`metadata.dgu.body`). `resource` means the asset links out to a resource, an external link with a URL. They are separate checks; a check the profile's `metadata_quality_dimensions` does not list yet is left out of the write instead of failing it.
- Placeholders created by lineage (stubs) are never written.
- `scores_written`, `score_conflicts` and `score_failures` in the run say how it went.

### System fields

A field marked `system: true` in the metamodel profile is written only by the platform. It must be bound to `metadata.*`, apply to assets only and be neither required, derived nor defaulted. The API refuses a write to it with the code `system` (a `PATCH`, like a derived field's `derived`); a whole-asset update, a creation or a discovery run leaves it as it was whatever they send, so a client that sends back the asset it read changes nothing. The interface shows it read-only. Mark `metadata_quality_score`, `metadata_quality_dimensions` and `metadata_quality_evaluated_at` with it in the profile; a server that does not know the key refuses the profile, so the profile and the server version go together.

## Not included yet

Rules declared in the profile (the coherence rules are fixed in the server today) are the next delivery. Reads are open to anyone with `metadata_quality:view`: when domains whose content only their members can read exist, the results will follow the same restriction as search.
