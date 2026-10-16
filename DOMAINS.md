# Domains (fork-only)

Domains and subdomains, with domain-scoped roles. Fork-only work on `dgu`; see the distribution's ADR-006 for the design and its implementation plan for the delivery order. This file is the maintenance contract: every change to a file that exists upstream is listed under **Seams**, and every write channel is classified under **Write inventory**. Review both on every `main → dgu` sync.

## Seams

Each row is a change to a file that also exists upstream. A PR that adds, moves or removes a seam updates this table in the same commit.

| File | Function / location | Why |
| --- | --- | --- |
| `internal/store/postgres/setup.go` | `Setup.Initialize`, last statement | Runs the fork migration track (`dgumigrations`, table `public.dgu_schema_version`) after the core one, so fork tables never take an upstream migration number |
| `internal/api/v1/server.go` | `New`, the `config.Domains.Enabled` block after the `server.handlers` list; two imports | Only with `domains.enabled`: registers `/api/v1/domains`, adds the ingestion observer to `asset.Service`, sets the search domain resolver, and refuses to start with the Elasticsearch search backend |
| `internal/api/v1/server.go` | `New`: the block after `asset.NewService` that builds the domain service and guard, and one `if domainGuard != nil` after each of `lineageService.NewService`, `assetdocs.NewService`, `glossaryService.NewService`, `dataProductSvc.SetMetamodel` and `assetruleService.NewService` | Wraps each service in its write decorator before any consumer receives it, so REST, OpenLineage, ingestion, agents and MCP all go through the guard |
| `internal/api/v1/server.go` | the `config.Domains.Enabled` block: replaces the docs, assets, data products and glossary handlers in `server.handlers` | Route middleware: `domainsAPI.GuardDocs` scopes documentation pages and `domainsAPI.WithCreateTargets` reads `?domain_id=` on the create routes. The handler list itself is untouched |
| `internal/api/v1/server.go` | `glossaryColumns` before `server.handlers`, passed to `glossaryImporter.New` | Adds the glossary import `domain` column when domains are enabled; `WithCreateTargets` also covers `/api/v1/glossary/import` |
| `internal/api/v1/assets/operations.go`, `assets/terms.go`, `assets/documentation.go`, `dataproducts/operations.go`, `glossary/operations.go`, `assetrules/operations.go`, `lineage/operations.go` | the error `switch` after each decorated write, and `respondAssetWriteError`; one import each | `domain.ErrForbidden` → 403 instead of 500 |
| `internal/core/runs/service.go` | `ProcessEntities`, first statement; one import | Puts the pipeline name in the context so new assets inherit the domain of the schedule with that name |
| `internal/core/lineage/service.go` | `service.edgeGuard` and the check at the top of `CreateDirectLineage`; the option lives in the new file `edge_guard.go` | Vets every edge, including those an OpenLineage event writes internally, which never go through a decorator |
| `internal/core/search/store.go` | `PostgresRepository.domainResolver`; `Search` resolves `@domain` first; `buildFilterClauses` and `buildListingFacetWhereClause` call `appendDomainClauses`; `buildFacetsParallel` skips cached facets when `filter.Domain` is set | `@domain` filter over subtrees, by id, name or name path, and `NOT @domain` exclusion. Cached facets are global counts and would ignore it |
| `internal/core/search/service.go` | `Filter.Domain` | Carries the resolved domain filter; never read from JSON |
| `pkg/config/config.go` | `Config.Domains`; `BindEnv` and `SetDefault` of `domains.enabled` and `domains.write_enforcement` | The feature flag (`MARMOT_DOMAINS_ENABLED`) and the state of write enforcement before it is ever set (`MARMOT_DOMAINS_WRITE_ENFORCEMENT`) |
| `internal/core/{asset,glossary,dataproduct,lineage,search,team,assetdocs,docs,assetrule,subscription,runs}/*store.go`, `internal/metrics/store.go` | the `db` field and its assignment in the constructor; one import | The pool is `readscope.Scoped`, so every read of the store leaves out what a restricted domain hides from the caller. No query changes |
| `internal/core/asset/store.go` | `Summary`; the default case of `GetMetadataValues` | `summary_counts` and `metadata_value_counts` total every asset, so a caller something is hidden from is counted live |
| `internal/core/asset/service.go` | `Summary`, `GetMetadataFields` | Their caches are shared by every caller: one something is hidden from neither reads nor fills them |
| `internal/core/search/store.go`, `external_service.go` | the condition before `buildCachedFacets`; `ExternalSearchService.Search` | Cached facets and the external index know nothing of domains: a caller something is hidden from is answered from PostgreSQL, uncached |
| `internal/core/notification/service.go` | `Service.assetReaders`, `SetAssetReaders`, first lines of `QueueAssetChange` | Drops the recipients of a notice about an asset who may not read it |
| `pkg/extension/host.go`, `internal/api/v1/extensions.go` | `Querier`, `ReadDB`; `extensionHost.ReadDB` | The scoped pool for what an extension reads on behalf of a caller |
| `pkg/config/config.go` | `Domains.RestrictedRead`; its `BindEnv` and `SetDefault` | `domains.restricted_read` (`MARMOT_DOMAINS_RESTRICTED_READ`), off by default: whether a domain may be marked restricted |
| `pkg/config/config_test.go` | `TestLoad_DCRAllowedRedirectHostsFromEnv` | Asserts the flag is read from the environment; `Load` runs once per process, so it cannot live in its own test |
| `permissions`, `role_permissions` (data, fork migration `002`) | rows `dgu_view_domains`, `dgu_manage_domains` | `domains:view` for `admin` and `user`, `domains:manage` for `admin`. Names are `dgu_`-prefixed so an upstream permission with the same name cannot collide |
| `charts/marmot/values.yaml`, `values.schema.json`, `templates/validation.yaml`, `tests/{configmap,validation}_test.yaml` | `config.domains`; the Elasticsearch check | The flag through the chart; refuses to render domains with Elasticsearch, as the server refuses to start |
| `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` | generated | Include the domain endpoints. On a sync conflict, regenerate with `make swagger` |

### Web seams

Fork-only UI lives in `web/marmot/src/lib/domains/`, `components/domain/` and `routes/domains/`. These are the upstream files it touches:

| File | Location | Why |
| --- | --- | --- |
| `web/marmot/messages/{en,es}.json` | `domains_*` keys | Paraglide catalogue; keys sorted, so a sync conflict is a line merge |
| `routes/+layout.svelte` | Governance menu entry and `pagetitle_domains` | Navigation |
| `routes/discover/+page.svelte` | `DomainFilter` in the filter column | `@domain` filter |
| `routes/discover/[type]/[service]/[name]/+page.svelte` | `DomainChip`; `domainWrite` joins `canManageAssets` | Shows the domain; hides edits the asset's domain does not allow |
| `routes/products/[id]/+page.svelte` | `DomainChip`; `domainWrite` joins `canManage` | Same for products |
| `routes/glossary/[[id]]/+page.svelte` | `DomainChip`, `DomainSelect` in the create modal with `?domain_id=` on the create request; `canEditTerm` for the selected term's edits | Same for terms; creating stays on `glossary:manage` |
| `routes/glossary/import/+page.svelte` | `DomainSelect` as the default domain, sent as `?domain_id=`; the `domain` column's label, format and error codes | Bulk import into domains |
| `components/asset/AssetBlade.svelte`, `components/product/ProductBlade.svelte` | read-only `DomainChip` | Domain in the Discover side panels |
| `components/product/DataProductForm.svelte`, `routes/assets/new/+page.svelte` | `DomainSelect`; `?domain_id=` on the create request | Domain on creation |
| `routes/pipelines/new/+page.svelte`, `routes/pipelines/[id]/edit/+page.svelte` | `PipelineDomain` | Pipeline domain, with the explicit asset move |
| `components/query/QueryBuilder.svelte`, `components/query/QueryInput.svelte` | `@domain` field, its values (shown by label, inserted by value) and its `=` operator | Query builder and `@` autocomplete |

## Read restriction

A domain marked `restricted` hides its content, and that of its subtree, from whoever holds no role in it or in a domain above it (delivery 3). Reading stays open everywhere else.

### How it decides

- `Guard.Hidden` (`internal/core/domain/read.go`) resolves, for the principal in the context, the domains of a restricted subtree no role of its own or of its teams reaches. Any role reads: `reader` exists for those who should do nothing else. The native `admin` role and the operator read everything, and so does work without a principal, which is the server's own.
- It is remembered per principal for two seconds, since every store read asks. A change to the tree or to its roles through the API forgets it at once on that server; another replica follows within those two seconds, and so does a change of team membership.
- Entities in Unassigned, or with no domain, are never hidden, and Unassigned cannot be restricted.
- A domain above one the caller reads stays in the tree, without its content, so the readable one can be reached. The API serves the tree through `domain.GuardReads`.

### How it is applied

- `internal/core/readscope` holds one condition per table that holds or names an asset, a glossary term or a data product (`visible`), and `readscope.Scoped` wraps a pool so that every `SELECT` naming one of them, inside a transaction too, reads it through a CTE of the same name without the hidden rows. A store that serves callers builds its pool with it; nothing else changes in its queries. Statements that write or lock are left alone.
- A caller nothing is hidden from runs the queries as upstream wrote them.
- Writes to what the caller may not read are refused by `Guard.AuthorizeEntities`, whether or not writes are scoped by domain.
- `TestEveryReaderOfADomainTableIsScoped` fails when a package reads one of those tables with a plain pool and is not listed, with its reason, in `unscoped`. A new table that refers to those entities gets its condition in `visible`; `TestEveryTableOfADomainHasACondition` runs each against the real schema.

### What is left out

| Channel | How |
| --- | --- |
| Assets, terms and products by ID, MRN or name; lists, search, facets, totals, suggestions, summary | scoped pool; live counts instead of the pre-computed ones |
| Lineage | edges with a hidden end are not read, so a traversal stops there and the neighbour is not a node |
| Terms of an asset, assets of a term, members and rules of a product, owners, tags, run history, documentation, subscriptions | scoped pool, through the tables that name the entity |
| MCP, OpenLineage, extensions' `Host.Assets()`, `Glossary()`, `Queries()` | they call the same services with the caller's principal |
| Notifications about an asset, and the team webhooks they feed | `SetAssetReaders`: only recipients with a role are told. An administrator without one is not |
| Domain tree, an entity's domain, roles of a domain | `GuardReads` |

### Limits

- `domains.restricted_read` is off by default and the API refuses the mark without it. The server filters either way; the option is the operator vouching that everything compiled into it does too. An extension reads the server's tables through `extension.ReadDB`, and one that keeps entity names or IDs in tables of its own must join the server's to filter them.
- Notifications delivered before a domain was restricted, or before a role was revoked, stay in their recipient's inbox.
- Usage metrics are aggregates over every asset; what they list by asset is filtered, their totals are not.
- `internal/core/quality` scores the whole catalogue for the server: whoever serves those scores filters them.

## Write inventory

Every operation that creates, changes, moves or deletes catalog content, and how domain-scoped write enforcement (delivery 2) covers it. `decorator` means the call goes through a service interface that `internal/core/domain` wraps in `server.go` (`decorators.go`).

### How enforcement decides

- The switch is the `write_enforcement` row in `domain_settings`, read on every write. Off, the decorators delegate without checking. The flag `domains.enabled` only turns the feature on; it never enforces anything by itself.
- On, a write needs `write` (steward or domain admin) on the entity's current domain; a new entity needs it on its destination: the `?domain_id=` target of the create request, else the pipeline's domain during ingestion, else Unassigned.
- `POST /api/v1/assets/`, `/products/` and `/glossary/` accept an optional `?domain_id=`, enforced or not. The entity is created and placed there, and the placement is audited (`create`); if it cannot be placed, it is deleted rather than left in Unassigned.
- The actor is the principal in the context. A scheduled run has none and writes with the scope of its schedule's domain (Unassigned if the schedule has none). A caller that names a pipeline over HTTP needs both its own scope and the pipeline's, so a pipeline name never grants anything.
- No principal and no pipeline: denied.
- Moving entities between domains (`PUT /domains/{id}/members`, pipeline reassignment) needs `write` on both ends. Every change of domain goes to `domain_audit_log`, enforced or not, and so do domain moves.

### Turning it on

- `GET /api/v1/domains/enforcement/plan` (global scope only) lists the identities with `assets:manage` or `glossary:manage`, outside the admin role, that would lose write access, with the topmost domains they keep and lose. It also lists the pipelines whose ingested assets sit outside their domain: their runs could no longer update or remove them.
- `POST /api/v1/domains/enforcement` with `{"write": true, "confirm": "<plan hash>"}` turns it on. A plan that changed since it was reviewed is refused with `plan_changed`. `{"write": false}` turns it off. Both need global scope and both are audited (`entity_kind = setting`).
- `GET /api/v1/domains/enforcement` returns the state to anyone with `domains:view`.
- `domains.write_enforcement` (`MARMOT_DOMAINS_WRITE_ENFORCEMENT`, off by default) is the state while the setting has never been saved, so an installation can start enforced. Once someone turns it on or off through the API, that choice wins and the configuration is no longer read.
- CLI: `marmot domains enforcement status | plan | enable --confirm <hash> | disable` (`internal/cmd/domains.go`, fork-only; it calls the API directly because domains are not in the generated SDK).

### Assets (`asset.Service`)

| Channel | Call site | Methods | Coverage |
| --- | --- | --- | --- |
| REST | `internal/api/v1/assets/operations.go`, `tags.go`, `terms.go` | Create, Update, PatchFields, Delete, AddTag, RemoveTag, AddTerms, RemoveTerm | decorator; linking a term changes the asset, so only the asset's domain counts |
| OpenLineage | `internal/core/lineage/openlineage.go` | Create, Update | decorator; the service account needs a role on the target domain (`unassigned` for new stubs) |
| Ingestion | `internal/core/runs/service.go` | Create, Update, DeleteByMRN, AddTerms | decorator with an explicit principal scoped to the run's schedule domain |
| MCP | `internal/mcp/write_tools.go` | Update (user description), AddTag, RemoveTag | decorator; MCP requests carry the caller's principal |

### Data products (`dataproduct.Service`)

| Channel | Call site | Methods | Coverage |
| --- | --- | --- | --- |
| REST | `internal/api/v1/dataproducts/operations.go` | Create, Update, Delete, AddAssets, RemoveAsset, UploadImage, DeleteImage | decorator; membership changes are checked against the **product's** domain |
| REST | same | CreateRule, UpdateRule, DeleteRule | global scope only while enforcement is on |
| Rule engine | `NewMembershipService` / `NewReconciler` (wired in `server.go`) | derived memberships | not scoped: derived from rules that only global scope can author |

### Glossary terms (`glossary.Service`)

| Channel | Call site | Methods | Coverage |
| --- | --- | --- | --- |
| REST | `internal/api/v1/glossary/operations.go` | Create, Update, Delete | decorator |
| REST (bulk import) | `internal/api/v1/glossary/import.go` | Import | decorator (`guardedGlossary.Import`): every row is authorized before the batch is written (new term where it lands: its `domain` cell, else `?domain_id=`, else Unassigned; existing term where it is, and at its `domain` cell when that moves it); after the batch commits each term is placed and audited. The `domain` column itself comes from `GlossaryImportColumns`, which checks it in the preview |
| Ingestion | `internal/core/runs/service.go` | SyncTerms | decorator; all or nothing: every existing term in the batch must be writable, and new terms land in the pipeline's domain |

### Lineage and documentation

| Channel | Call site | Methods | Coverage |
| --- | --- | --- | --- |
| REST, ingestion | `internal/api/v1/lineage` (`/lineage/direct`, `/lineage/batch`), `internal/core/runs/service.go` | `lineage.Service` CreateDirectLineage, DeleteDirectLineage | decorator (`GuardLineage`): the **target** asset's domain decides; the downstream side declares what it reads. An MRN with no asset yet counts as Unassigned |
| Agents | `internal/core/agent/service.go` | `lineage.Service` BatchObservedLineage | decorator; all edges or none. The run record is kept and the agent gets the error text, as for any lineage failure |
| OpenLineage | `internal/core/lineage/openlineage.go` | edges written inside `ProcessOpenLineageEvent` | `lineage.WithEdgeGuard`: each edge by its target, like any other; a refused edge is skipped with a warning and the rest of the event is processed |
| REST | `internal/api/v1/assets/documentation.go` | `assetdocs.Service` Create, CreateGlobal | decorator (`GuardAssetDocs`): the asset's domain; global documentation needs global scope |
| REST | `internal/api/v1/docs` (pages and images) | `docs.Service`, a concrete type | `domainsAPI.GuardDocs` adds a check as the innermost middleware of every write route: the page's owner (asset by MRN, or data product) decides |
| Asset rules | `internal/api/v1/assetrules` | `assetrule.Service` Create, Update, Delete | decorator (`GuardAssetRules`): global scope while enforcement is on. Their evaluation writes derived links and is not scoped |

Out of scope: subscriptions and notifications (per-user), users, roles, teams, SSO and service accounts (global administration).

## Known gaps

- Elasticsearch: `@domain` is only applied by the Postgres search backend, so the server refuses to start with `domains.enabled` and Elasticsearch together.
