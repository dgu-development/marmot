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
| `Host.Domains()` | Whether the caller may write in or administer a domain; nil on a server without domains |

Grow `Host` when an extension needs something, with that extension as the proof. Behaviour of the core itself (search, metamodel validation, domain enforcement) is not an extension: it stays a patch here.

## Seams

| File | Location | Why |
| --- | --- | --- |
| `internal/store/postgres/setup.go` | `Setup.Initialize`, last statement; one import | Runs each extension's migration track after the fork's |
| `internal/api/v1/server.go` | `New`, the block after the domains one | Appends the extensions' routes to `server.handlers` |

`pkg/extension`, `internal/extensions` and `internal/api/v1/extensions.go` are new files.
