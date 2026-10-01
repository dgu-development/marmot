<script lang="ts">
	import { resolve } from '$app/paths';
	import IconifyIcon from '@iconify/svelte';
	import { m } from '$lib/paraglide/messages';
	import { assetPath, resolveAssets, type AssetRef } from '$lib/assets/links';

	let {
		ids,
		onremove = undefined
	}: {
		ids: string[];
		/** Shows a remove button on each chip. */
		onremove?: (id: string) => void;
	} = $props();

	let assets = $state<Map<string, AssetRef | null>>(new Map());

	$effect(() => {
		let cancelled = false;
		resolveAssets(ids)
			.then((resolved) => {
				if (!cancelled) assets = resolved;
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	});
</script>

<div class="flex flex-wrap gap-1.5">
	{#each ids as id (id)}
		{@const asset = assets.get(id)}
		{@const path = asset ? assetPath(asset) : null}
		<span
			class="inline-flex max-w-full items-center gap-1 rounded-full bg-earthy-terracotta-100 px-2 py-0.5 text-xs text-earthy-terracotta-700 transition-opacity has-[a:hover]:opacity-80 dark:bg-earthy-terracotta-900 dark:text-earthy-terracotta-100"
		>
			{#if asset && path}
				<a
					href={resolve(`/discover/${path.type}/${path.provider}/${path.name}`)}
					class="truncate"
					title={asset.type}>{asset.name}</a
				>
			{:else if assets.has(id)}
				<span class="truncate italic" title={id}>{m.metamodel_asset_link_deleted()}</span>
			{:else}
				<span class="truncate opacity-60">…</span>
			{/if}
			{#if onremove}
				<button
					type="button"
					onclick={() => onremove(id)}
					class="rounded-full p-0.5 hover:bg-earthy-terracotta-200 dark:hover:bg-earthy-terracotta-800"
					aria-label={m.metamodel_list_remove({ value: asset?.name ?? id })}
				>
					<IconifyIcon icon="material-symbols:close-rounded" class="h-3 w-3" />
				</button>
			{/if}
		</span>
	{/each}
</div>
