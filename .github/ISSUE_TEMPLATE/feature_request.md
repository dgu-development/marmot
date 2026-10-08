---
name: Feature request
about: Propose a change to Marmot (API, Discover, plugins, or generic kernel behaviour)
title: '[feat] '
labels: 'enhancement'
assignees: ''
---

## Problem

<!--
ES: Qué no puedes hacer hoy con Marmot. No pidas una extensión (`dguext-<id>`) ni un overlay: van en su repositorio y en la distribución.
EN: What you cannot do in Marmot today. Do not ask for an extension (`dguext-<id>`) or an overlay: those live in their own repository and in the consuming distribution.
-->



## Proposed change

<!--
ES: Comportamiento genérico reutilizable. Por ejemplo, perfil YAML del metamodelo customizado y locales ES/EN no se commitean aquí.
EN: Reusable generic behaviour. For example, corporate YAML metamodel profile and ES/EN catalogues do not land in this repo.
-->



## Where it should live

- [ ] Native data already (asset types, metadata, glossary, products, rules, lineage) — maybe docs or a small API/UI fix. Product PRs still target `dgu`.
- [ ] Discovery plugin (`plugin-sdk`, ingest only: no HTTP, no UI, no RBAC)
- [ ] Extension (`dguext-<id>`, compiled in through `pkg/extension`: own routes under `/api/v1/ext/<id>/`, own `ext_<id>_*` tables) — not this repo, unless the contract has to grow
- [ ] Kernel Go (service, store, migration, `/api/v1`): it changes the behaviour of the core
- [ ] Discover UI (`web/marmot`, Svelte 5; no second UI framework)

<!--
ES: Un plugin no registra rutas ni autorización. Una extensión añade; no modifica el núcleo (EXTENSIONS.md).
EN: A plugin does not register routes or authorization. An extension adds; it does not modify the core (EXTENSIONS.md).
-->

Why a thinner option is not enough:



## Alternatives considered

<!--
ES: Glosario, productos, reglas de activo, metadata libre, conector existente, cambio de config.
EN: Glossary, data products, asset rules, free-form metadata, an existing connector, or config.
-->



## Identity / metadata

- [ ] Does not change MRNs
- [ ] Does not turn semantic relations into lineage
- [ ] Preserves unknown metadata and native ownership relations
