<script lang="ts">
	import {
		entityPanels,
		panelsFor,
		type EntityRef,
		type PanelPlacement
	} from '$lib/extensions/entity-panels';

	/**
	 * `withTabs` shows tab panels here too, for pages without a tab bar. `placement` "header" shows
	 * only the panels registered for the page header.
	 */
	let {
		entity,
		withTabs = false,
		placement = 'side'
	}: {
		entity: EntityRef;
		withTabs?: boolean;
		placement?: PanelPlacement;
	} = $props();

	const shown = $derived(
		placement === 'side'
			? [
					...panelsFor($entityPanels, entity.kind, 'side'),
					...(withTabs ? panelsFor($entityPanels, entity.kind, 'tab') : [])
				]
			: panelsFor($entityPanels, entity.kind, placement)
	);
</script>

{#each shown as panel (panel.id)}
	{#await panel.load() then module}
		<module.default {entity} {placement} />
	{/await}
{/each}
