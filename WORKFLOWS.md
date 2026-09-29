# Workflows (fork-only)

Governance workflows modelled in BPMN 2.0: approvals, reviews and escalations over assets. Fork-only work on `dgu`; the design is the distribution's plan `docs/plans/20260929-flujos-y-notificaciones.md` (`DEV_16`). The editor, inbox and run views live in the distribution module `dgu-web-module-workflows`, not in this tree. Off unless `workflows.enabled` (`MARMOT_WORKFLOWS_ENABLED`).

## What runs

The engine executes a declared subset of BPMN and refuses anything else with a stable issue code. It never runs code carried by a diagram.

| Element | Behaviour |
| --- | --- |
| `startEvent` (none) | One per process. Runs are started by a user, with an optional target asset |
| `userTask` | Waits for a decision. Assignment: `camunda:assignee`, `camunda:candidateUsers` (username or id; `${initiator}`), `camunda:candidateGroups` (`team:<name or id>`, `role:<domain role>` for the target's domain, `role:<role>@<domain id>`) |
| `serviceTask` | One platform action in `dgu:action`: `notify` (`dgu:message`, `dgu:to` = `initiator` or `participants`), `set_field` (`dgu:field`, `dgu:value`, through the metamodel `PatchFields`), `add_tag` (`dgu:tag`) |
| `exclusiveGateway` | First outgoing flow whose condition holds, else `default`. Conditions are `variable == value` / `!=` joined by `&&`, optionally in `${…}`; nothing else parses |
| `parallelGateway` | Fork, and join when every incoming flow arrived |
| `boundaryEvent` with `timerEventDefinition` | On a user task; ISO 8601 `timeDuration` in weeks, days, hours, minutes, seconds. Interrupting cancels the task; `cancelActivity="false"` opens a second path |
| `endEvent` (none, terminate) | Terminate cancels every open task |

Lanes, pools, annotations, groups and extension elements are accepted and ignored. `scriptTask` is refused (`script_not_allowed`); timer and message start events, subprocesses, call activities and error, signal and compensation events are refused as unsupported. The distribution module lists the same subset.

Completing a task sets `decision` and `<task id>.decision`; the instance also carries `initiator`, `target_kind`, `target_id` and `target_name`.

## Rules

- **Identity.** A service task writes as the user who advanced the run (who started it or decided the task before it), never as a service credential. `set_field` and `add_tag` need `assets:manage`, and the domain write guard applies as for any request. A path reached from a timer has no user, so a write there fails the run (`action_failed`) instead of borrowing one.
- **Deciding.** A task's candidates are resolved when it opens (for the inbox) and **again** when someone decides it: a revoked role or a left team cannot decide. Native admins may decide any task. A task with no candidate is logged as `task_unassigned` and waits for an admin.
- **Versions.** A definition is a draft until published; published and retired versions are frozen, and runs pin the version they started on. A draft may be saved with issues; publishing requires none. The process id ties versions together and cannot change.
- **Visibility.** Runs are visible to their initiator, to anyone who has or had a task in them, and to `workflows:manage`. Anyone else gets 404, not 403.
- **Consistency.** Each step runs in one transaction with a row lock and a revision check on the run. Notifications are queued after commit. An action's effect on an asset is not rolled back if the transaction later fails; `set_field` is idempotent, `notify` may repeat.
- **Failure.** A failing action, a gateway with no matching branch, a loop that never waits (1000 steps) or a join that can never complete fails the run, cancels its open tasks and records the code, element and message.

## Notifications

In-app, through the native notification service, so every channel it gets later applies without change here: `task_assigned` (candidates of a new task), `task_escalated` (candidates of a task opened by a timer), `workflow_decision` (initiator, when the run ends), `workflow_message` (`notify` action). The payload carries `instance_id`, `workflow`, `asset_name` and a `link` to the module.

## API

Every route needs `workflows:view`; managing definitions, listing every run (`all=true`) and cancelling need `workflows:manage`, checked by the service. Errors carry a stable `code`; diagram errors also carry `issues`.

| Route | Purpose |
| --- | --- |
| `GET/POST /api/v1/workflows/definitions` | List (managers: all versions; others: published), create a draft from `{bpmn}` |
| `POST /api/v1/workflows/definitions/validate` | `{valid, issues}` for a document, without storing it |
| `GET/PUT/DELETE /api/v1/workflows/definitions/{id}` | Read, replace or delete a draft (409 once published) |
| `GET /api/v1/workflows/definitions/{id}/bpmn` | Export the BPMN |
| `POST /api/v1/workflows/definitions/{id}/publish`, `/retire` | Freeze a valid draft; stop new runs of a version |
| `GET/POST /api/v1/workflows/instances` | List visible runs; start `{definition_id, target: {kind: "asset", id}}` |
| `GET /api/v1/workflows/instances/{id}` | Run with tasks, events, diagram and active nodes |
| `POST /api/v1/workflows/instances/{id}/cancel` | Cancel a run |
| `GET /api/v1/workflows/tasks` | The caller's open tasks |
| `POST /api/v1/workflows/tasks/{id}/complete` | `{decision, comment}` |

## Schema

Fork migration `009_workflows.sql`: `workflow_definitions`, `workflow_instances` (state as JSONB, `revision` for compare-and-set), `workflow_tasks` (resolved `candidates`, due date and timer), `workflow_events` (append-only audit). Permissions `dgu_view_workflows` (`admin`, `user`) and `dgu_manage_workflows` (`admin`). Timers fire from a cluster singleton (`workflow-timers`, `workflows.timer_interval`, default one minute).

## Seams

| File | Location | Why |
| --- | --- | --- |
| `internal/api/v1/server.go` | `Server.workflowTimers`; the `config.Workflows.Enabled` block after the domains block; `Stop`; three imports | Builds the service on the guarded asset service, starts the timers, registers the handler |
| `pkg/config/config.go` | `Config.Workflows`; `BindEnv` and `SetDefault` for `workflows.enabled` and `workflows.timer_interval`; `time` import | The feature flag |
| `pkg/config/config_test.go` | `TestLoad_DCRAllowedRedirectHostsFromEnv` | Asserts both keys are read from the environment |
| `permissions`, `role_permissions` (data, fork migration `009`) | rows `dgu_view_workflows`, `dgu_manage_workflows` | Same `dgu_` rule as domains |
| `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` | generated | Include the workflow endpoints; regenerate with `make swagger` |

Not yet: the Helm chart does not expose `workflows.*`, and there is no start from platform events.
