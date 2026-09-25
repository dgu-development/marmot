<script lang="ts">
	import { resolve } from '$app/paths';
	import { m } from '$lib/paraglide/messages';

	let { values, size = 'xs' }: { values: string[]; size?: 'xs' | 'sm' } = $props();
</script>

<div class="flex flex-wrap gap-1.5">
	{#each values as value, i (i)}
		<a
			href={resolve(`/discover?q=${encodeURIComponent(value)}`)}
			class="rounded-full bg-earthy-terracotta-100 px-2 py-0.5 break-all text-earthy-terracotta-700 hover:underline dark:bg-earthy-terracotta-900 dark:text-earthy-terracotta-100 {size ===
			'sm'
				? 'text-sm'
				: 'text-xs'}"
			title={m.metamodel_search_for({ value })}>{value}</a
		>
	{/each}
</div>
