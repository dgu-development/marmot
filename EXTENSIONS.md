# Extensions (fork-only)

A capability with its own routes and tables can live outside this repository and be compiled into the server. The contract is `pkg/extension`; nothing under `internal/` is importable from another module, so what an extension needs from the server is published there, and only there.

## How one is linked

An extension is a Go module that calls `extension.Register` from an `init` function. The image build generates a file of blank imports from the distribution's lock, so the binary contains exactly the extensions the lock names. Nothing is loaded at run time.

## What it gets

| | |
| --- | --- |
| Routes | Served under `/api/v1/ext/<id>/`. Every route requires an authenticated caller; `Resource` and `Action` add a permission check |
| Tables | Its own migration track (`public.ext_<id>_schema_version`, tern format), applied after the core and fork tracks. Tables are named `ext_<id>_*` |
| `Host.DB()` | The pool. Core tables are read through it; writing them from an extension is an exception to justify in its own repository, not the rule |
| `Host.Principal` | Who is calling |
| `Host.Domains()` | Whether the caller may write in or administer a domain, the domain of an entity and who holds each role in it; nil on a server without domains |
| `Host.Users()`, `Host.Teams()` | People and teams by ID or name, and the members of a team |
| `Host.Assets()` | An asset by ID and the writes of the API: profile fields with the version check, tags and glossary terms. They run the same guards as the API, as the person the context carries |
| `Host.Glossary()`, `Host.Queries()` | A term by name; the assets a Discover query matches |
| `Host.Quality()` | The evaluating half of the metadata quality audit: the profile in force and its rules, validating a rule a person wrote, and judging a batch of assets with given settings and custom rules, writing their scores as the platform. Its data is `pkg/extension/quality` |
| `Host.Notifications()` | In-app notifications, delivered through the channels each recipient chose |
| `Host.As` | A person's identity for work without a request: the principal and a context the write guards read |
| `Host.Schedule` | A task repeated on one replica at a time, stopped with the server |

The host's services answer with the contract's own types and errors (`ErrNotFound`, `ErrForbidden`, `ErrVersionConflict`, `*FieldsError`), never with types of `internal/`. An extension does not declare permissions: a route names one that exists.

Grow `Host` when an extension needs something, with that extension as the proof. Behaviour of the core itself (search, metamodel validation, domain enforcement) is not an extension: it stays a patch here.

## Seams

| File | Location | Why |
| --- | --- | --- |
| `internal/store/postgres/setup.go` | `Setup.Initialize`, last statement; one import | Runs each extension's migration track after the fork's |
| `internal/api/v1/server.go` | `New`, the block after the domains one | Appends the extensions' routes to `server.handlers` |

`pkg/extension`, `internal/extensions` and `internal/api/v1/extensions.go` are new files.

## In use

The versions of a domain's ontology were added here first (`ontology_versions`, `/api/v1/ontologies/...`) and moved out to the `dguext-ontology` extension, which owns `ext_ontology_versions` and serves under `/api/v1/ext/ontology/`. Fork migration `020` drops the table they left behind.

The users, teams, assets, glossary, queries, notifications, `As` and `Schedule` of the host were added for the workflow engine, which needs all of them to assign tasks, write the target asset as the person who decided and run its timers. It moved out to `dguext-workflows` ([WORKFLOWS.md](WORKFLOWS.md)), which takes over the `workflow_*` tables as `ext_workflows_*`.

A new extension starts from [dguext-template](https://github.com/dgu-development/dguext-template).
