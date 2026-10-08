# Workflows

The BPMN workflow engine left this fork for its extension, [dguext-workflows](https://github.com/dgu-development/dguext-workflows): its `server/` half holds the engine and serves under `/api/v1/ext/workflows/`; `docs/engine.md` there is the contract this file used to be.

What stays here:

- Fork migrations `009_workflows.sql` and `010_workflows_iteration.sql`. They create the `workflow_*` tables, which the extension renames to `ext_workflows_*` with their data, and insert the permissions `workflows:view`, `workflows:start` and `workflows:manage`, which an extension cannot declare.
- The labels of those permissions in the role editor.

`workflows.enabled` and `workflows.timer_interval` are gone: the engine runs when the extension is compiled in.
