<script lang="ts">
	import IconifyIcon from '@iconify/svelte';
	import { m } from '$lib/paraglide/messages';
	import { createKeyboardNavigationState } from '$lib/keyboard';
	import { findTerms, rememberTerm, type TermRef } from '$lib/glossary/links';
	import TermLinks from './TermLinks.svelte';

	let {
		ids,
		multiple,
		exclude = undefined,
		inputId,
		labelledby,
		describedby = undefined,
		onchange,
		onescape = undefined
	}: {
		ids: string[];
		multiple: boolean;
		/** A term that may not be picked, such as the one being edited. */
		exclude?: string;
		inputId: string;
		labelledby: string;
		describedby?: string;
		onchange: (ids: string[]) => void;
		onescape?: () => void;
	} = $props();

	let query = $state('');
	let results = $state<TermRef[]>([]);
	let searching = $state(false);
	let focused = $state(-1);
	let timer: ReturnType<typeof setTimeout>;

	const open = $derived(query.trim().length >= 2);
	const canAdd = $derived(multiple || ids.length === 0);

	function search(value: string) {
		query = value;
		clearTimeout(timer);
		if (value.trim().length < 2) {
			results = [];
			focused = -1;
			return;
		}
		timer = setTimeout(async () => {
			searching = true;
			try {
				results = (await findTerms(value)).filter((t) => t.id !== exclude && !ids.includes(t.id));
			} catch {
				results = [];
			} finally {
				searching = false;
				focused = -1;
			}
		}, 250);
	}

	function pick(term: TermRef) {
		rememberTerm(term);
		onchange(multiple ? [...ids, term.id] : [term.id]);
		query = '';
		results = [];
		focused = -1;
	}

	const nav = createKeyboardNavigationState(
		() => results,
		() => focused,
		(i) => (focused = i),
		{
			onSelect: pick,
			onEscape: () => (query ? search('') : onescape?.())
		}
	);

	function focusOnMount(node: HTMLElement) {
		node.focus();
	}
</script>

<div class="space-y-1.5">
	{#if ids.length > 0}
		<TermLinks {ids} onremove={(id) => onchange(ids.filter((entry) => entry !== id))} />
	{/if}
	{#if canAdd}
		<div class="relative">
			<input
				id={inputId}
				type="text"
				class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 pl-8 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
				placeholder={m.glossary_link_search_placeholder()}
				aria-labelledby={labelledby}
				aria-describedby={describedby}
				aria-expanded={open}
				role="combobox"
				aria-controls={`${inputId}-listbox`}
				autocomplete="off"
				value={query}
				oninput={(e) => search(e.currentTarget.value)}
				onkeydown={nav.handleKeydown}
				use:focusOnMount
			/>
			<IconifyIcon
				icon="material-symbols:search-rounded"
				class="pointer-events-none absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2 text-gray-400"
			/>
			{#if open}
				<div
					id={`${inputId}-listbox`}
					role="listbox"
					class="absolute z-10 mt-1 max-h-60 w-full min-w-64 overflow-auto rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
				>
					{#if searching}
						<div class="px-3 py-3 text-sm text-gray-500 dark:text-gray-400">
							{m.owners_searching()}
						</div>
					{:else if results.length === 0}
						<div class="px-3 py-3 text-sm text-gray-500 dark:text-gray-400">
							{m.glossary_no_terms_found()}
						</div>
					{:else}
						{#each results as term, i (term.id)}
							<button
								type="button"
								role="option"
								aria-selected={i === focused}
								onclick={() => pick(term)}
								class="block w-full px-3 py-2 text-left text-sm transition-colors {i === focused
									? 'bg-gray-100 dark:bg-gray-700'
									: 'hover:bg-gray-50 dark:hover:bg-gray-700/50'}"
							>
								<span class="block truncate font-medium text-gray-900 dark:text-gray-100"
									>{term.name}</span
								>
								<span class="block truncate text-xs text-gray-500 dark:text-gray-400"
									>{term.definition}</span
								>
							</button>
						{/each}
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</div>
