<script lang="ts">
	import { entityPanels, panelsFor, type EntityRef } from '$lib/extensions/entity-panels';

	/** `withTabs` shows tab panels here too, for pages without a tab bar. */
	let { entity, withTabs = false }: { entity: EntityRef; withTabs?: boolean } = $props();

	const shown = $derived([
		...panelsFor($entityPanels, entity.kind, 'side'),
		...(withTabs ? panelsFor($entityPanels, entity.kind, 'tab') : [])
	]);
</script>

{#each shown as panel (panel.id)}
	{#await panel.load() then module}
		<module.default {entity} placement="side" />
	{/await}
{/each}
