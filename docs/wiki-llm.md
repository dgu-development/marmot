# WikiLLM en el fork

La ruta `/wiki` es una sección principal del portal. Enumera activos, productos de datos, términos del glosario y, si están habilitados, dominios. Cada ficha muestra primero la descripción completa, las secciones documentales y las memorias; una columna lateral reúne metadatos y relaciones. Las fuentes quedan disponibles al final para comprobar la procedencia. La pestaña de memorias de activos y productos procede de [marmotdata/marmot#293](https://github.com/marmotdata/marmot/pull/293), incorporada en `feature/wikillm` con `cherry-pick -x`.

## Qué compila

El servicio `internal/core/knowledge` lee atributos del catálogo y del metamodelo efectivo, documentación nativa e importada, memorias, pertenencia a productos y dominios, enlaces con términos, jerarquías y linaje. Cada fragmento tiene un ID estable y un hash. El resultado se guarda en `knowledge_pages`, separado de las fuentes y de las páginas editadas por usuarios; un resultado generado nunca vuelve a entrar como fuente en una compilación posterior.

«Compilar» es la operación interna que reúne esas fuentes, detecta qué ha cambiado y prepara un resumen con citas. Es incremental. Al arrancar y en cada intervalo, el servidor recopila y publica automáticamente páginas deterministas a partir del catálogo, la documentación, el metamodelo y las relaciones, aunque todavía no existan memorias ni interacciones MCP. Si cambian esas fuentes, vuelve a compilar y publicar la página. El lector no necesita ejecutar esta operación. Las síntesis generadas por un modelo permanecen como borradores para revisión humana. Una página publicada con síntesis no se reemplaza automáticamente por una recopilación determinista si se desactiva el proveedor. `POST /api/v1/knowledge/pages/{entityType}/{entityId}/publish` exige el `draft_hash` exacto revisado y que las fuentes sigan siendo las mismas; si cambiaron, devuelve `409`.

El modo predeterminado recopila evidencia en Markdown, con citas y sin llamadas externas. Para generar una síntesis, configure un endpoint OpenAI-compatible de **chat completions** en el servidor:

```yaml
knowledge:
  endpoint: https://provider.example/v1/chat/completions
  model: model-name
  interval_seconds: 900
```

La clave se configura como secreto `MARMOT_KNOWLEDGE_API_KEY`. También se admiten `MARMOT_KNOWLEDGE_ENDPOINT`, `MARMOT_KNOWLEDGE_MODEL` y `MARMOT_KNOWLEDGE_INTERVAL_SECONDS`. El intervalo `0` desactiva la compilación programada; con un valor positivo, el servidor empieza una pasada al arrancar y repite la comprobación con ese intervalo. Un bloqueo de PostgreSQL evita que varias réplicas ejecuten la pasada a la vez. Las llamadas al proveedor usan el contexto de cancelación y tienen un tiempo máximo de 90 segundos. El cuerpo de las fuentes se entrega al proveedor configurado, por lo que la selección de ese proveedor forma parte de la política de datos del despliegue.

Los contenidos generados por el modelo son propuestas. Se exige un JSON de párrafos con IDs de fuente existentes; se rechazan citas inventadas y respuestas incompletas. Esto comprueba la procedencia formal de cada párrafo, no demuestra que todas sus afirmaciones sean correctas. La revisión humana sigue siendo necesaria.

## Acceso

La lectura requiere `assets:view` y `glossary:view`, más `domains:view` si están habilitados los dominios. Los editores con `assets:manage` pueden crear y editar secciones de activos y productos mediante la API de documentación existente, sometida al control de dominio de la entidad. Los términos y dominios muestran su definición, metadatos y relaciones; su edición sigue en la ficha del catálogo. Compilar y publicar síntesis requiere también `knowledge:write`; la migración lo concede inicialmente solo a `admin`. Con escritura por dominio activada, esas operaciones exigen ámbito global porque una página puede combinar fuentes de varias entidades. MCP añade `get_knowledge_context`, `read_knowledge` y `compile_knowledge` con los mismos controles. El contexto de agentes contiene únicamente páginas publicadas y vigentes. `remember` y las otras operaciones de memoria de #293 conservan `memory:write`, además del control de escritura del dominio de la entidad.

La lectura restringida por dominio sigue pendiente en el fork. WikiLLM aplica los permisos de lectura actuales del catálogo; antes de activar restricciones de lectura por dominio se deberán comprobar también las fuentes indirectas y la búsqueda de páginas. No se utiliza WikiLLM para datos que exijan una separación de lectura que el catálogo todavía no garantiza.

## API y migraciones

- `GET /api/v1/knowledge` enumera entidades y estados, con `q`, `limit` y `offset`.
- `GET /api/v1/knowledge/pages/{entityType}/{entityId}` lee una página.
- `POST /api/v1/knowledge/pages/{entityType}/{entityId}/compile` genera o reutiliza un borrador.
- `POST /api/v1/knowledge/pages/{entityType}/{entityId}/publish` publica la revisión exacta.
- `GET /api/v1/knowledge/context?q=...` recupera páginas publicadas vigentes.
- `POST /api/v1/knowledge/compile?limit=1&offset=N` permite pasar por el catálogo en lotes acotados; la actualización ordinaria es automática.

Las migraciones del fork `007`–`009` portan la memoria de #293, y `010` crea las páginas de conocimiento. Se aplican después de las migraciones base mediante `dgu_schema_version`. Esta separación evita ocupar números de migración que upstream puede usar en futuras versiones. Al integrar #293 en upstream habrá que reconciliar sus migraciones con las del fork. El rollback de la imagen no revierte automáticamente los datos publicados.

## Comprobación

Ejecute `go test ./internal/core/knowledge ./internal/core/domain ./internal/core/memory ./internal/mcp ./internal/store/postgres/dgumigrations` con `MARMOT_TEST_POSTGRES_DSN` para las pruebas de integración. El frontend usa `pnpm --dir web/marmot build`. El `svelte-check` global conserva errores ajenos a esta funcionalidad; hay que inspeccionar los archivos tocados.
