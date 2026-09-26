<script lang="ts">
	import IconifyIcon from '@iconify/svelte';
	import Checkbox from '$components/ui/Checkbox.svelte';
	import { m } from '$lib/paraglide/messages';

	type Item = { value: string; label: string; count: number };

	let {
		title,
		items,
		selected,
		ontoggle,
		limit = 6
	}: {
		title: string;
		items: Item[];
		selected: string[];
		ontoggle: (value: string, checked: boolean) => void;
		limit?: number;
	} = $props();

	const SEARCH_FROM = 10;

	let open = $state(true);
	let expanded = $state(false);
	let query = $state('');

	// Checked values stay on top, so a long facet never hides what is filtering.
	const sorted = $derived(
		[...items].sort(
			(a, b) =>
				Number(selected.includes(b.value)) - Number(selected.includes(a.value)) ||
				b.count - a.count ||
				a.label.localeCompare(b.label)
		)
	);
	const matching = $derived.by(() => {
		const needle = query.trim().toLowerCase();
		return needle ? sorted.filter((item) => item.label.toLowerCase().includes(needle)) : sorted;
	});
	const shown = $derived(
		expanded || query.trim() ? matching : matching.slice(0, Math.max(limit, selected.length))
	);
	const hidden = $derived(matching.length - shown.length);
	const max = $derived(Math.max(1, ...items.map((item) => item.count)));
	const titleId = $props.id();
</script>

<section class="mb-4" aria-labelledby={titleId}>
	<button
		type="button"
		class="group mb-2 flex w-full items-center gap-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 rounded"
		aria-expanded={open}
		onclick={() => (open = !open)}
	>
		<h3
			id={titleId}
			class="text-xs font-semibold tracking-wider text-gray-600 uppercase dark:text-gray-400"
		>
			{title}
		</h3>
		<span
			class="rounded-full bg-gray-100 px-1.5 text-[10px] text-gray-500 tabular-nums dark:bg-gray-700 dark:text-gray-400"
			>{items.length}</span
		>
		{#if selected.length}
			<span
				class="rounded-full bg-earthy-terracotta-100 px-1.5 text-[10px] font-medium text-earthy-terracotta-800 dark:bg-earthy-terracotta-900/40 dark:text-earthy-terracotta-200"
				>{selected.length}</span
			>
		{/if}
		<IconifyIcon
			icon="material-symbols:keyboard-arrow-down-rounded"
			class="ml-auto h-4 w-4 text-gray-400 transition-transform {open ? '' : '-rotate-90'}"
		/>
	</button>

	{#if open}
		{#if items.length > SEARCH_FROM}
			<div class="relative mb-2">
				<IconifyIcon
					icon="material-symbols:search-rounded"
					class="pointer-events-none absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400"
				/>
				<input
					type="search"
					bind:value={query}
					placeholder={m.discover_facet_search({ title: title.toLowerCase() })}
					aria-label={m.discover_facet_search({ title: title.toLowerCase() })}
					class="w-full rounded-md border border-gray-200 bg-white py-1 pr-2 pl-7 text-xs text-gray-800 placeholder:text-gray-400 focus:border-earthy-terracotta-500 focus:ring-1 focus:ring-earthy-terracotta-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200"
				/>
			</div>
		{/if}

		<ul class="space-y-0.5">
			{#each shown as item (item.value)}
				{@const checked = selected.includes(item.value)}
				<li>
					<label
						class="group/row relative flex cursor-pointer items-center gap-2 overflow-hidden rounded-md px-1.5 py-1 hover:bg-gray-50 dark:hover:bg-gray-800/60"
					>
						<span
							class="pointer-events-none absolute inset-y-0.5 left-0 rounded-r-md {checked
								? 'bg-earthy-terracotta-500/15'
								: 'bg-gray-400/10 dark:bg-white/5'}"
							style:width="{(item.count / max) * 100}%"
							aria-hidden="true"
						></span>
						<Checkbox
							class="relative"
							{checked}
							onchange={(event) => ontoggle(item.value, event.currentTarget.checked)}
						/>
						<span
							class="relative min-w-0 flex-1 truncate text-sm {checked
								? 'font-medium text-gray-900 dark:text-gray-100'
								: 'text-gray-700 dark:text-gray-300'}"
							title={item.label}>{item.label}</span
						>
						<span class="relative text-xs text-gray-500 tabular-nums dark:text-gray-400"
							>{item.count}</span
						>
					</label>
				</li>
			{:else}
				<li class="px-1.5 py-1 text-xs text-gray-400 italic">{m.discover_facet_none()}</li>
			{/each}
		</ul>

		{#if hidden > 0 || (expanded && !query.trim() && matching.length > limit)}
			<button
				type="button"
				class="mt-1 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium text-earthy-terracotta-700 hover:bg-earthy-terracotta-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 dark:text-earthy-terracotta-400 dark:hover:bg-gray-800"
				onclick={() => (expanded = !expanded)}
			>
				<IconifyIcon
					icon={expanded
						? 'material-symbols:expand-less-rounded'
						: 'material-symbols:expand-more-rounded'}
					class="h-4 w-4"
				/>
				{expanded ? m.discover_facet_show_less() : m.discover_facet_show_more({ count: hidden })}
			</button>
		{/if}
	{/if}
</section>
