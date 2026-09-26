<script lang="ts">
	import { entityPanels, type EntityRef } from '$lib/extensions/entity-panels';

	/** `tab` is a page tab id, `ext-<panel id>`. */
	let { tab, entity }: { tab: string; entity: EntityRef } = $props();

	const panel = $derived($entityPanels.find((item) => `ext-${item.id}` === tab));
</script>

{#if panel}
	{#await panel.load() then module}
		<module.default {entity} placement="tab" />
	{/await}
{/if}
