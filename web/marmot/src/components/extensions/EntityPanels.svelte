<script lang="ts">
	import { entityPanels, type EntityRef } from '$lib/extensions/entity-panels';

	let { entity }: { entity: EntityRef } = $props();

	const shown = $derived(
		$entityPanels.filter((panel) => !panel.kinds || panel.kinds.includes(entity.kind))
	);
</script>

{#each shown as panel (panel.id)}
	{#await panel.load() then module}
		<module.default {entity} />
	{/await}
{/each}
