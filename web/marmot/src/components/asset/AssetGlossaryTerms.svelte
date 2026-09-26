<script lang="ts">
	import { fetchApi } from '$lib/api';
	import { resolve } from '$app/paths';
	import type { Asset, AssetTerm, GlossaryTerm } from '$lib/assets/types';
	import { auth } from '$lib/stores/auth';
	import { m } from '$lib/paraglide/messages';

	let { asset, editable = true }: { asset: Asset; editable?: boolean } = $props();

	let canManageAssets = $derived(editable && auth.hasPermission('assets', 'manage'));

	let terms: AssetTerm[] = $state([]);
	let showTermPicker = $state(false);
	let availableTerms: GlossaryTerm[] = $state([]);
	let termSearchQuery = $state('');
	let loadingTerms = $state(false);
	let savingTerm = $state(false);

	$effect(() => {
		if (asset?.id) {
			fetchAssetTerms();
		}
	});

	async function fetchAssetTerms() {
		try {
			const response = await fetchApi(`/assets/terms/${asset.id}`);
			if (response.ok) {
				const data = await response.json();
				terms = Array.isArray(data) ? data : [];
			}
		} catch (error) {
			console.error('Failed to fetch asset terms:', error);
		}
	}

	async function fetchAvailableTerms() {
		if (!termSearchQuery.trim()) {
			availableTerms = [];
			return;
		}

		loadingTerms = true;
		try {
			const response = await fetchApi(`/glossary/search?q=${encodeURIComponent(termSearchQuery)}`);
			if (response.ok) {
				const data = await response.json();
				availableTerms = data.terms || [];
			}
		} catch (error) {
			console.error('Failed to fetch glossary terms:', error);
		} finally {
			loadingTerms = false;
		}
	}

	async function addTerm(termId: string) {
		if (!asset?.id) return;

		savingTerm = true;
		try {
			const response = await fetchApi(`/assets/terms/${asset.id}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					term_ids: [termId]
				})
			});

			if (response.ok) {
				const data = await response.json();
				terms = Array.isArray(data) ? data : [];
				showTermPicker = false;
				termSearchQuery = '';
				availableTerms = [];
			} else {
				console.error('Failed to add term');
			}
		} catch (error) {
			console.error('Error adding term:', error);
		} finally {
			savingTerm = false;
		}
	}

	async function removeTerm(termId: string) {
		if (!asset?.id) return;

		try {
			const response = await fetchApi(`/assets/terms/${asset.id}`, {
				method: 'DELETE',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					term_id: termId
				})
			});

			if (response.ok) {
				const data = await response.json();
				terms = Array.isArray(data) ? data : [];
			} else {
				console.error('Failed to remove term');
			}
		} catch (error) {
			console.error('Error removing term:', error);
		}
	}

	function getSourceTooltip(source: string): string {
		if (source === 'user') return m.asset_user_notes_manual_title();
		if (source.startsWith('rule:')) return m.asset_term_source_rule_title();
		return m.asset_term_source_plugin_title({ plugin: source.replace('plugin:', '') });
	}

	function isRuleManaged(source: string): boolean {
		return source.startsWith('rule:');
	}

	const FIRST = 5;
	const SEARCH_FROM = 8;
	let expanded = $state(false);
	let filter = $state('');
	const matching = $derived.by(() => {
		const needle = filter.trim().toLowerCase();
		if (!needle) return terms;
		return terms.filter(
			(term) =>
				term.term_name.toLowerCase().includes(needle) ||
				(term.definition ?? '').toLowerCase().includes(needle)
		);
	});
	const shownTerms = $derived(expanded || filter.trim() ? matching : matching.slice(0, FIRST));
</script>

{#if terms.length > 0 || canManageAssets}
	<div>
		<div class="flex items-center justify-between mb-2">
			<h3 class="flex items-center gap-2 text-base font-semibold text-gray-900 dark:text-gray-100">
				{m.asset_glossary_terms_heading()}
				{#if terms.length > 0}
					<span
						class="rounded-full bg-gray-100 px-2 text-xs font-medium text-gray-600 tabular-nums dark:bg-gray-700 dark:text-gray-300"
						>{terms.length}</span
					>
				{/if}
			</h3>
			{#if canManageAssets && !showTermPicker}
				<button
					onclick={() => (showTermPicker = true)}
					class="text-sm text-earthy-terracotta-700 dark:text-earthy-terracotta-700 hover:text-earthy-terracotta-700 dark:hover:text-earthy-terracotta-400 font-medium"
				>
					{m.asset_add_button()}
				</button>
			{/if}
		</div>

		{#if terms.length > 0 || showTermPicker}
			{#if terms.length > SEARCH_FROM}
				<input
					type="search"
					bind:value={filter}
					placeholder={m.asset_terms_filter()}
					aria-label={m.asset_terms_filter()}
					class="mb-2 w-full rounded-md border border-gray-200 bg-white px-2.5 py-1.5 text-sm text-gray-800 placeholder:text-gray-400 focus:border-earthy-terracotta-500 focus:ring-1 focus:ring-earthy-terracotta-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200"
				/>
			{/if}
			<!-- Long lists open with a few terms and the rest on demand, so the column stays short. -->
			<div class="space-y-1.5">
				{#each shownTerms as term (term.term_id)}
					<div class="rounded border border-gray-200 dark:border-gray-700">
						<a
							href={resolve(`/glossary/${term.term_id}`)}
							class="flex items-start gap-2 px-3 py-2 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors group"
							title={term.definition}
						>
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2">
									<h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">
										{term.term_name}
									</h4>
									<div
										class="flex items-center justify-center w-5 h-5 rounded-full {term.source ===
										'user'
											? 'bg-blue-100 dark:bg-blue-900/30'
											: isRuleManaged(term.source)
												? 'bg-teal-100 dark:bg-teal-900/30'
												: 'bg-purple-100 dark:bg-purple-900/30'}"
										title={getSourceTooltip(term.source)}
									>
										{#if term.source === 'user'}
											<svg
												class="w-3 h-3 text-blue-700 dark:text-blue-300"
												fill="currentColor"
												viewBox="0 0 20 20"
											>
												<path
													fill-rule="evenodd"
													d="M10 9a3 3 0 100-6 3 3 0 000 6zm-7 9a7 7 0 1114 0H3z"
													clip-rule="evenodd"
												/>
											</svg>
										{:else if isRuleManaged(term.source)}
											<svg
												class="w-3 h-3 text-teal-700 dark:text-teal-300"
												fill="currentColor"
												viewBox="0 0 20 20"
											>
												<path
													fill-rule="evenodd"
													d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
													clip-rule="evenodd"
												/>
											</svg>
										{:else}
											<svg
												class="w-3 h-3 text-purple-700 dark:text-purple-300"
												fill="currentColor"
												viewBox="0 0 20 20"
											>
												<path
													fill-rule="evenodd"
													d="M11.3 1.046A1 1 0 0112 2v5h4a1 1 0 01.82 1.573l-7 10A1 1 0 018 18v-5H4a1 1 0 01-.82-1.573l7-10a1 1 0 011.12-.38z"
													clip-rule="evenodd"
												/>
											</svg>
										{/if}
									</div>
								</div>
								{#if term.definition}
									<p class="mt-0.5 line-clamp-1 text-xs text-gray-600 dark:text-gray-400">
										{term.definition}
									</p>
								{/if}
							</div>
							<div class="flex items-center gap-1.5 flex-shrink-0">
								<svg
									class="w-3.5 h-3.5 text-gray-400 group-hover:text-earthy-terracotta-700"
									fill="none"
									stroke="currentColor"
									viewBox="0 0 24 24"
								>
									<path
										stroke-linecap="round"
										stroke-linejoin="round"
										stroke-width="2"
										d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
									/>
								</svg>
								{#if canManageAssets && term.source === 'user'}
									<button
										onclick={(e) => {
											e.preventDefault();
											e.stopPropagation();
											removeTerm(term.term_id);
										}}
										class="text-gray-400 hover:text-red-600 dark:hover:text-red-400"
										aria-label={m.asset_remove_term_aria()}
									>
										<svg class="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
											<path
												fill-rule="evenodd"
												d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z"
												clip-rule="evenodd"
											/>
										</svg>
									</button>
								{/if}
							</div>
						</a>
					</div>
				{/each}

				{#if matching.length > FIRST && !filter.trim()}
					<button
						type="button"
						class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium text-earthy-terracotta-700 hover:bg-earthy-terracotta-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 dark:text-earthy-terracotta-400 dark:hover:bg-gray-800"
						onclick={() => (expanded = !expanded)}
					>
						{expanded
							? m.discover_facet_show_less()
							: m.discover_facet_show_more({ count: matching.length - FIRST })}
					</button>
				{/if}

				{#if showTermPicker}
					<div
						class="p-3 rounded bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-600"
					>
						<input
							type="text"
							bind:value={termSearchQuery}
							oninput={fetchAvailableTerms}
							placeholder={m.asset_term_search_placeholder()}
							class="w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 focus:ring-1 focus:ring-earthy-terracotta-600 focus:border-transparent mb-2"
							autofocus
						/>

						{#if loadingTerms}
							<div class="flex items-center justify-center py-4">
								<div
									class="animate-spin rounded-full h-4 w-4 border-b-2 border-earthy-terracotta-700"
								></div>
							</div>
						{:else if availableTerms.length > 0}
							<div class="space-y-1.5 max-h-48 overflow-y-auto">
								{#each availableTerms as availableTerm (availableTerm.id)}
									{@const alreadyAdded = terms.some((t) => t.term_id === availableTerm.id)}
									<button
										onclick={() => !alreadyAdded && addTerm(availableTerm.id)}
										disabled={alreadyAdded || savingTerm}
										class="w-full text-left p-2 rounded border border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50 disabled:cursor-not-allowed"
									>
										<div class="text-sm font-medium text-gray-900 dark:text-gray-100">
											{availableTerm.name}
											{#if alreadyAdded}
												<span class="text-xs text-gray-500 ml-1.5"
													>{m.asset_term_already_added()}</span
												>
											{/if}
										</div>
										<div class="text-xs text-gray-600 dark:text-gray-400 mt-0.5 line-clamp-2">
											{availableTerm.definition}
										</div>
									</button>
								{/each}
							</div>
						{:else if termSearchQuery.trim()}
							<p class="text-xs text-gray-500 dark:text-gray-400 text-center py-3">
								{m.asset_no_matching_terms()}
							</p>
						{/if}

						<div class="flex justify-end gap-1.5 mt-2">
							<button
								onclick={() => {
									showTermPicker = false;
									termSearchQuery = '';
									availableTerms = [];
								}}
								class="px-2 py-1 text-xs text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800 rounded"
							>
								{m.common_cancel()}
							</button>
						</div>
					</div>
				{/if}
			</div>
		{:else}
			<p class="text-sm text-gray-500 dark:text-gray-400 italic">
				{m.asset_no_glossary_terms_yet()}
			</p>
		{/if}
	</div>
{/if}
