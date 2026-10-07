<script lang="ts">
	import { onMount, afterUpdate } from 'svelte';
	import { writable, type Writable } from 'svelte/store';
	import { SvelteURLSearchParams } from 'svelte/reactivity';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { fetchApi } from '$lib/api';
	import { toasts, parseApiError, isLimitExceeded } from '$lib/stores/toast';
	import type {
		GlossaryTerm,
		TermsListResponse,
		Owner,
		UpdateTermInput
	} from '$lib/glossary/types';
	import QueryInput from '$components/query/QueryInput.svelte';
	import MarkdownRenderer from '$components/ui/MarkdownRenderer.svelte';
	import RichTextEditor from '$components/editor/RichTextEditor.svelte';
	import OwnerSelector from '$components/shared/OwnerSelector.svelte';
	import DomainChip from '$components/domain/DomainChip.svelte';
	import DomainSelect from '$components/domain/DomainSelect.svelte';
	import { entityWritable } from '$lib/domains/writable';
	import Button from '$components/ui/Button.svelte';
	import Icon from '@iconify/svelte';
	import Tags from '$components/shared/Tags.svelte';
	import MetadataView from '$components/shared/MetadataView.svelte';
	import ProductGovernedFields from '$components/product/ProductGovernedFields.svelte';
	import TermReferences from '$components/glossary/TermReferences.svelte';
	import TermLinkSummary from '$components/glossary/TermLinkSummary.svelte';
	import FieldBadges from '$components/metamodel/FieldBadges.svelte';
	import SearchLinks from '$components/metamodel/SearchLinks.svelte';
	import { GLOSSARY_TERM_CONTROL, linkIds } from '$lib/glossary/links';
	import { nativeMessage } from '$lib/metamodel/i18n';
	import { resolveMessage, valueLabel } from '$lib/metamodel/labels';
	import { locale } from '$lib/i18n';
	import { fetchMetamodel } from '$lib/metamodel/api';
	import type { MetamodelSchema } from '$lib/metamodel/types';
	import {
		governedFields,
		governedPaths,
		readMetadataValue,
		toPayload,
		writeMetadataValue
	} from '$lib/metamodel/values';
	import { auth } from '$lib/stores/auth';
	import { m } from '$lib/paraglide/messages';
	import { formatDate } from '$lib/utils';
	import EntityPanels from '$components/extensions/EntityPanels.svelte';

	const terms: Writable<GlossaryTerm[]> = writable([]);
	const totalTerms: Writable<number> = writable(0);
	const isLoading: Writable<boolean> = writable(true);
	const error: Writable<string | null> = writable(null);

	let searchQuery = $page.url.searchParams.get('q') || '';
	let searchTimeout: ReturnType<typeof setTimeout>;
	// The URL is the search's persisted copy. It only overwrites the box when it changes on its own
	// (back, a link, the global search): its echo of what was just typed would undo later keystrokes.
	let urlQuery = searchQuery;
	let pushedQuery: string | null = null;

	let selectedTerm: GlossaryTerm | null = null;
	let showCreateModal = false;
	let showDeleteConfirm = false;
	let referencingCount = 0;

	let newTermName = '';
	let newTermDefinition = '';
	let newTermDescription = '';
	let newTermOwners: Owner[] = [];
	let newTermDomain = '';
	let isCreating = false;
	let createError = '';

	let isEditing = false;
	let editedTerm: GlossaryTerm | null = null;

	let metamodel: MetamodelSchema | null = null;
	$: governed = metamodel?.enabled ? governedFields(metamodel.fields) : [];
	$: governedHidePaths = governedPaths(governed);
	$: termTypeField = governed.find((field) => field.id === 'term_type');
	$: detailFields = governed.filter((field) => field.id !== 'term_type');
	const defaultTermType = 'business_term';
	$: termTypeValue = termTypeField
		? String(
				readMetadataValue(
					(isEditing && editedTerm ? editedTerm.metadata : selectedTerm?.metadata) ?? {},
					termTypeField.storage
				) ?? ''
			) || defaultTermType
		: '';
	let termTypeOpen = false;
	$: if (selectedTerm && !Array.isArray(selectedTerm.tags)) selectedTerm.tags = [];

	$: linkFields = governed.filter((f) => f.presentation?.control === GLOSSARY_TERM_CONTROL);

	function messageContext() {
		return {
			locale: $locale,
			defaultLocale: metamodel?.defaultLocale ?? 'es',
			messages: metamodel?.messages,
			native: nativeMessage
		};
	}

	function linkLabel(key: string | undefined, fallback: string): string {
		if (!metamodel) return fallback;
		return (
			resolveMessage(key, {
				locale: $locale,
				defaultLocale: metamodel.defaultLocale,
				messages: metamodel.messages,
				native: nativeMessage
			}) ?? fallback
		);
	}

	function synonymsOf(term: GlossaryTerm | null): string[] {
		const value = term?.metadata?.synonyms;
		return Array.isArray(value) ? value.filter((s): s is string => typeof s === 'string') : [];
	}

	function matchedSynonym(term: GlossaryTerm, query: string): string | undefined {
		const q = query.trim().toLowerCase();
		if (!q || term.name.toLowerCase().includes(q)) return undefined;
		return synonymsOf(term).find((s) => s.toLowerCase().includes(q));
	}

	const canManageGlossary = auth.hasPermission('glossary', 'manage');
	// Creating stays on canManageGlossary; editing also needs the selected term's domain.
	$: termWritable = entityWritable('glossary_term', selectedTerm?.id);
	$: canEditTerm = canManageGlossary && $termWritable;
	let didAutoSelect = false;

	$: {
		const query = $page.url.searchParams.get('q') || '';
		if (query !== urlQuery) {
			urlQuery = query;
			if (query !== pushedQuery) searchQuery = query;
		}

		const termId = $page.params.id;
		if (termId && (!selectedTerm || selectedTerm.id !== termId)) {
			const term = $terms.find((t) => t.id === termId);
			if (term) {
				selectedTerm = term;
			}
			// Term not in the loaded list: a separate page.subscribe handler in
			// onMount fetches it. Calling loadTermById from this reactive block
			// would assign selectedTerm and re-trigger the block.
		} else if (!termId && !selectedTerm && !didAutoSelect && $terms.length > 0) {
			// Latch the auto-select to a single fire so the assignment to
			// selectedTerm via selectTerm() doesn't loop this reactive block.
			didAutoSelect = true;
			selectTerm($terms[0]);
		}
	}

	async function fetchTerms() {
		isLoading.set(true);
		error.set(null);

		try {
			const queryParams = new SvelteURLSearchParams({
				limit: '100',
				offset: '0'
			});

			if (searchQuery) {
				queryParams.append('q', searchQuery);
			}

			const endpoint = searchQuery ? '/glossary/search' : '/glossary/list';
			const response = await fetchApi(`${endpoint}?${queryParams}`);

			if (!response.ok) {
				const errorData = await response.json();
				throw new Error(errorData.error || m.glossary_fetch_error());
			}

			const data: TermsListResponse = await response.json();
			const processedTerms = (data.terms || []).map((term) => ({
				...term,
				tags: term.tags || [],
				metadata: term.metadata || {}
			}));
			terms.set(processedTerms);
			totalTerms.set(data.total || 0);
		} catch (err) {
			error.set(err instanceof Error ? err.message : m.glossary_fetch_error());
		} finally {
			isLoading.set(false);
		}
	}

	function handleSearch(query: string) {
		clearTimeout(searchTimeout);
		searchQuery = query;

		searchTimeout = setTimeout(() => {
			pushedQuery = query;
			const url = new URL(window.location.href);
			if (query) {
				url.searchParams.set('q', query);
			} else {
				url.searchParams.delete('q');
			}
			goto(resolve(`${url.pathname}${url.search}${url.hash}`), {
				replaceState: true,
				noScroll: true,
				keepFocus: true
			});
		}, 300);
	}

	function handleSearchSubmit() {
		clearTimeout(searchTimeout);
		pushedQuery = searchQuery;
		const url = new URL(window.location.href);
		if (searchQuery) {
			url.searchParams.set('q', searchQuery);
		} else {
			url.searchParams.delete('q');
		}
		goto(resolve(`${url.pathname}${url.search}${url.hash}`), {
			replaceState: true,
			noScroll: true,
			keepFocus: true
		});
	}

	function selectTerm(term: GlossaryTerm) {
		selectedTerm = term;
		referencingCount = 0;
		isEditing = false;
		editedTerm = null;

		// Update URL with selected term using path parameter
		const searchParams = $page.url.searchParams.toString();
		const url = searchParams ? `/glossary/${term.id}?${searchParams}` : `/glossary/${term.id}`;
		goto(resolve(url), { replaceState: true, noScroll: true, keepFocus: true });
	}

	async function loadTermById(termId: string) {
		try {
			const response = await fetchApi(`/glossary/${termId}`);
			if (response.ok) {
				const term: GlossaryTerm = await response.json();
				// Initialize tags and metadata if they don't exist
				if (!term.tags) term.tags = [];
				if (!term.metadata) term.metadata = {};
				selectedTerm = term;
			}
		} catch (err) {
			console.error('Failed to load term:', err);
		}
	}

	function handleNewTerm() {
		newTermName = '';
		newTermDefinition = '';
		newTermDescription = '';
		newTermOwners = [];
		newTermDomain = '';
		createError = '';
		showCreateModal = true;
	}

	async function createTerm() {
		if (!newTermName || !newTermDefinition) {
			createError = m.glossary_error_name_definition_required();
			return;
		}

		isCreating = true;
		createError = '';

		try {
			const owners =
				newTermOwners.length > 0
					? newTermOwners.map((o) => ({ id: o.id, type: o.type }))
					: undefined;

			const target = newTermDomain ? `?domain_id=${encodeURIComponent(newTermDomain)}` : '';
			const response = await fetchApi(`/glossary/${target}`, {
				method: 'POST',
				body: JSON.stringify({
					name: newTermName,
					definition: newTermDefinition,
					description: newTermDescription || undefined,
					owners,
					metadata: termTypeField
						? writeMetadataValue({}, termTypeField.storage, defaultTermType)
						: { dgu: { term_type: defaultTermType } }
				})
			});

			if (!response.ok) {
				const info = await parseApiError(response);
				if (isLimitExceeded(info)) toasts.warning(info.message);
				throw new Error(info.message);
			}

			showCreateModal = false;
			fetchTerms();
		} catch (err) {
			createError = err instanceof Error ? err.message : m.glossary_create_error();
		} finally {
			isCreating = false;
		}
	}

	function startEdit() {
		if (!selectedTerm) return;
		isEditing = true;
		termTypeOpen = false;
		editedTerm = JSON.parse(JSON.stringify(selectedTerm));
		if (!termTypeField || !editedTerm) return;
		const current = String(
			readMetadataValue(editedTerm.metadata ?? {}, termTypeField.storage) ?? ''
		);
		if (!current) {
			editedTerm.metadata = writeMetadataValue(
				editedTerm.metadata ?? {},
				termTypeField.storage,
				defaultTermType
			);
		}
	}

	function cancelEdit() {
		isEditing = false;
		termTypeOpen = false;
		editedTerm = null;
	}

	function chooseTermType(value: string) {
		termTypeOpen = false;
		if (!editedTerm || !termTypeField || value === termTypeValue) return;
		const parsed = toPayload(termTypeField, value);
		if (!parsed.ok) return;
		editedTerm.metadata = writeMetadataValue(
			editedTerm.metadata ?? {},
			termTypeField.storage,
			parsed.value
		);
	}

	function badgeMetadata(term: GlossaryTerm | null) {
		if (!term || !termTypeField) return term?.metadata;
		const current = String(readMetadataValue(term.metadata ?? {}, termTypeField.storage) ?? '');
		if (current) return term.metadata;
		return writeMetadataValue(term.metadata ?? {}, termTypeField.storage, defaultTermType);
	}

	async function saveEdit() {
		if (!selectedTerm || !editedTerm) return;

		try {
			const updateData: UpdateTermInput = {};

			if (editedTerm.name !== selectedTerm.name) {
				updateData.name = editedTerm.name;
			}
			if (editedTerm.definition !== selectedTerm.definition) {
				updateData.definition = editedTerm.definition;
			}
			if (editedTerm.description !== selectedTerm.description) {
				updateData.description = editedTerm.description || null;
			}

			const currentOwners = selectedTerm.owners.map((o) => ({ id: o.id, type: o.type }));
			const newOwners = editedTerm.owners.map((o) => ({ id: o.id, type: o.type }));
			const ownersChanged =
				newOwners.length !== currentOwners.length ||
				!newOwners.every((no) =>
					currentOwners.some((co) => co.id === no.id && co.type === no.type)
				);

			if (ownersChanged) {
				updateData.owners = newOwners;
			}

			// Check if metadata changed
			const metadataChanged =
				JSON.stringify(editedTerm.metadata) !== JSON.stringify(selectedTerm.metadata);
			if (metadataChanged) {
				updateData.metadata = editedTerm.metadata;
			}

			if (Object.keys(updateData).length === 0) {
				cancelEdit();
				return;
			}

			const response = await fetchApi(`/glossary/${selectedTerm.id}`, {
				method: 'PUT',
				body: JSON.stringify(updateData)
			});

			if (!response.ok) {
				const errorData = await response.json();
				throw new Error(errorData.error || m.glossary_update_error());
			}

			applyTerm(await response.json());

			isEditing = false;
			editedTerm = null;
		} catch (err) {
			error.set(err instanceof Error ? err.message : m.glossary_update_error());
		}
	}

	function applyTerm(updated: GlossaryTerm) {
		if (!updated.tags) updated.tags = [];
		if (!updated.metadata) updated.metadata = {};
		selectedTerm = updated;
		terms.update((list) => list.map((t) => (t.id === updated.id ? updated : t)));
	}

	async function deleteTerm() {
		if (!selectedTerm) return;

		try {
			const response = await fetchApi(`/glossary/${selectedTerm.id}`, {
				method: 'DELETE'
			});

			if (!response.ok) {
				const errorData = await response.json();
				throw new Error(errorData.error || m.glossary_delete_error());
			}

			showDeleteConfirm = false;
			selectedTerm = null;
			fetchTerms();
		} catch (err) {
			error.set(err instanceof Error ? err.message : m.glossary_delete_error());
		}
	}

	afterUpdate(() => {
		document.title = selectedTerm?.name
			? m.glossary_term_page_title({ name: selectedTerm.name })
			: m.glossary_page_title();
	});

	onMount(() => {
		fetchMetamodel('glossary_term')
			.then((schema) => (metamodel = schema))
			.catch(() => (metamodel = null));

		// Refetch whenever the page URL changes (search params, route id, etc).
		// Subscribing here instead of using a `$:` block avoids the lint's
		// infinite-reactive-loop heuristic, since fetchTerms() mutates stores
		// the rest of this component reads.
		let lastFetchedTermId: string | null | undefined = undefined;
		const unsubscribe = page.subscribe((p) => {
			if (!p?.url) return;
			fetchTerms();

			// Lazy-load any term whose id is in the URL but not yet known.
			const termId = p.params?.id;
			if (termId && termId !== lastFetchedTermId && termId !== selectedTerm?.id) {
				lastFetchedTermId = termId;
				loadTermById(termId);
			}
		});
		return () => unsubscribe();
	});
</script>

<svelte:window on:click={() => (termTypeOpen = false)} />

<div class="h-[calc(100vh-4rem)] overflow-y-auto">
	<div class="max-w-[1600px] mx-auto px-6 py-6">
		<div class="flex gap-8">
			<!-- Left Sidebar -->
			<div class="w-80 flex-shrink-0 flex flex-col gap-4">
				<div
					class="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-4"
				>
					<div class="flex items-center justify-between mb-4">
						<h1 class="text-xl font-bold text-gray-900 dark:text-gray-100">
							{m.glossary_heading()}
						</h1>
						{#if canManageGlossary}
							<div class="flex items-center gap-1">
								<a
									href={resolve('/glossary/import')}
									class="rounded-lg p-2 text-gray-500 hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-200"
									aria-label={m.glossary_import_link()}
									title={m.glossary_import_link()}
								>
									<Icon icon="material-symbols:upload-file-outline" class="h-5 w-5" />
								</a>
								<Button
									click={handleNewTerm}
									icon="material-symbols:add"
									variant="filled"
									class="!p-2"
								/>
							</div>
						{/if}
					</div>

					<QueryInput
						value={searchQuery}
						onQueryChange={handleSearch}
						onSubmit={handleSearchSubmit}
						placeholder={m.glossary_search_placeholder()}
					/>

					<div class="text-xs text-gray-500 dark:text-gray-400 mt-3">
						{m.glossary_term_count({ count: $totalTerms })}
					</div>
				</div>

				<div
					class="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden flex-1 min-h-0"
				>
					{#if $isLoading}
						<div class="flex justify-center items-center h-32">
							<div
								class="animate-spin rounded-full h-8 w-8 border-b-2 border-earthy-terracotta-700 dark:border-earthy-terracotta-500"
							></div>
						</div>
					{:else if $error}
						<div
							class="m-4 rounded-lg bg-red-50 dark:bg-red-900/20 p-3 text-sm text-red-800 dark:text-red-200"
						>
							{$error}
						</div>
					{:else if $terms.length === 0}
						<div class="py-12 text-center">
							<Icon
								icon="material-symbols:book-outline"
								class="mx-auto h-12 w-12 text-gray-400 dark:text-gray-600"
							/>
							<p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
								{searchQuery ? m.glossary_no_terms_found() : m.glossary_no_terms_yet()}
							</p>
						</div>
					{:else}
						<div class="overflow-y-auto max-h-full">
							{#each $terms as term (term.id)}
								<button
									on:click={() => selectTerm(term)}
									class="w-full px-4 py-3 text-left transition-all border-l-4 {selectedTerm?.id ===
									term.id
										? 'bg-earthy-terracotta-50 dark:bg-earthy-terracotta-900/20 border-earthy-terracotta-700 dark:border-earthy-terracotta-500'
										: 'hover:bg-gray-50 dark:hover:bg-gray-700/50 border-transparent'}"
								>
									<div class="font-medium text-gray-900 dark:text-gray-100 text-sm">
										{term.name}
									</div>
									{#if matchedSynonym(term, searchQuery)}
										<div
											class="mt-0.5 text-xs text-earthy-terracotta-700 dark:text-earthy-terracotta-400"
										>
											{m.glossary_synonym_match({
												synonym: matchedSynonym(term, searchQuery) ?? ''
											})}
										</div>
									{/if}
									{#each linkFields as field (field.id)}
										{@const ids = linkIds(readMetadataValue(term.metadata, field.storage))}
										{#if ids.length > 0}
											<TermLinkSummary
												label={linkLabel(field.presentation?.labelKey, field.id)}
												{ids}
											/>
										{/if}
									{/each}
									<div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400 line-clamp-1">
										{term.definition}
									</div>
								</button>
							{/each}
						</div>
					{/if}
				</div>
			</div>

			<!-- Right Detail Panel -->
			<div class="flex-1">
				{#if selectedTerm}
					<div>
						<div
							class="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden"
						>
							<!-- Header Section -->
							<div class="relative px-6 py-5 border-b border-gray-200 dark:border-gray-700">
								<div class="mb-3 flex items-start justify-between gap-3">
									<div class="min-w-0 flex-1">
										{#if isEditing && editedTerm}
											<input
												type="text"
												bind:value={editedTerm.name}
												class="w-full text-2xl font-bold bg-transparent border-b-2 border-earthy-terracotta-300 pb-2 text-gray-900 focus:border-earthy-terracotta-700 focus:outline-none dark:border-earthy-terracotta-700 dark:text-gray-100 dark:focus:border-earthy-terracotta-500"
												placeholder={m.glossary_term_name_placeholder()}
											/>
										{:else}
											<h2 class="text-2xl font-bold text-gray-900 dark:text-gray-100">
												{selectedTerm.name}
											</h2>
										{/if}
										{#if isEditing && editedTerm && termTypeField}
											<div class="relative mt-3 flex flex-wrap items-center gap-2">
												<span
													id="glossary-term-type-label"
													class="text-xs font-semibold tracking-wider text-gray-500 uppercase dark:text-gray-400"
												>
													{linkLabel(termTypeField.presentation?.labelKey, termTypeField.id)}
												</span>
												<div class="relative inline-flex">
													<button
														id="glossary-term-type"
														type="button"
														aria-haspopup="listbox"
														aria-expanded={termTypeOpen}
														aria-labelledby="glossary-term-type-label glossary-term-type"
														on:click|stopPropagation={() => (termTypeOpen = !termTypeOpen)}
														class="inline-flex items-center gap-2 rounded-md border border-gray-300 bg-white px-3 py-2 text-left text-sm text-gray-900 focus:border-transparent focus:ring-2 focus:ring-earthy-terracotta-500 focus:outline-none dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
													>
														<span class="min-w-0 truncate"
															>{valueLabel(termTypeField, termTypeValue, messageContext()) ??
																termTypeValue}</span
														>
														<Icon
															icon="material-symbols:keyboard-arrow-down"
															class="h-4 w-4 shrink-0 text-gray-500 transition-transform dark:text-gray-400 {termTypeOpen
																? 'rotate-180'
																: ''}"
														/>
													</button>
													{#if termTypeOpen}
														<div
															role="listbox"
															aria-labelledby="glossary-term-type-label"
															class="absolute top-full left-0 z-50 mt-1 max-h-60 w-max min-w-full overflow-y-auto overscroll-contain rounded-md border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-700 dark:bg-gray-800"
														>
															{#each termTypeField.values ?? [] as value (value)}
																<button
																	type="button"
																	role="option"
																	aria-selected={value === termTypeValue}
																	on:click|stopPropagation={() => chooseTermType(value)}
																	class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors {value ===
																	termTypeValue
																		? 'font-medium text-earthy-terracotta-700 dark:text-earthy-terracotta-700'
																		: 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'}"
																>
																	<span
																		>{valueLabel(termTypeField, value, messageContext()) ??
																			value}</span
																	>
																	{#if value === termTypeValue}
																		<span
																			class="h-1.5 w-1.5 shrink-0 rounded-full bg-earthy-terracotta-700"
																			aria-hidden="true"
																		></span>
																	{/if}
																</button>
															{/each}
														</div>
													{/if}
												</div>
											</div>
										{:else if !isEditing}
											<div class="mt-2 flex flex-shrink-0 flex-wrap gap-1.5">
												<FieldBadges schema={metamodel} metadata={badgeMetadata(selectedTerm)} />
											</div>
										{/if}
									</div>
									{#if canEditTerm}
										<div class="flex flex-shrink-0 items-center gap-2">
											{#if isEditing}
												<Button
													click={saveEdit}
													icon="material-symbols:check"
													text={m.glossary_save_changes()}
													variant="filled"
												/>
												<Button click={cancelEdit} text={m.common_cancel()} variant="clear" />
											{:else}
												<button
													type="button"
													on:click={startEdit}
													class="inline-flex items-center gap-2 rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
												>
													<Icon icon="material-symbols:edit-outline" class="h-4 w-4" />
													{m.common_edit()}
												</button>
											{/if}
										</div>
									{/if}
								</div>
								{#if !isEditing && synonymsOf(selectedTerm).length > 0}
									<div
										class="-mt-1 mb-3 flex flex-wrap items-center gap-1.5"
										aria-label={m.glossary_synonyms_label()}
									>
										<span class="text-xs text-gray-500 dark:text-gray-400"
											>{m.glossary_synonyms_label()}:</span
										>
										<SearchLinks values={synonymsOf(selectedTerm)} />
									</div>
								{/if}

								<!-- Definition -->
								{#if isEditing && editedTerm}
									<textarea
										bind:value={editedTerm.definition}
										rows="2"
										class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-gray-600 rounded-lg focus:outline-none focus:ring-2 focus:ring-earthy-terracotta-600 focus:border-transparent dark:bg-gray-700 dark:text-gray-100 resize-none"
										placeholder={m.glossary_definition_edit_placeholder()}
									></textarea>
								{:else}
									<p class="text-sm text-gray-600 dark:text-gray-400 leading-relaxed">
										{selectedTerm.definition}
									</p>
								{/if}
							</div>

							<!-- Body Section -->
							<div class="p-6 space-y-5">
								<!-- Owners Section -->
								<div>
									<div class="flex items-center gap-2 mb-2">
										<Icon
											icon="material-symbols:person-outline"
											class="w-4 h-4 text-gray-500 dark:text-gray-400"
										/>
										<h3
											class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider"
										>
											{m.common_owners()}
										</h3>
									</div>
									{#if isEditing && editedTerm}
										<OwnerSelector
											bind:selectedOwners={editedTerm.owners}
											onChange={(owners) => {
												if (editedTerm) {
													editedTerm.owners = owners;
												}
											}}
										/>
									{:else}
										<OwnerSelector
											selectedOwners={selectedTerm.owners}
											onChange={() => {}}
											disabled={true}
										/>
									{/if}
								</div>

								<DomainChip
									kind="glossary_term"
									entityId={selectedTerm.id}
									canEdit={canEditTerm}
									variant="section"
								/>

								<!-- Tags Section -->
								<div>
									<div class="flex items-center gap-2 mb-2">
										<Icon
											icon="material-symbols:label-outline"
											class="w-4 h-4 text-gray-500 dark:text-gray-400"
										/>
										<h3
											class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider"
										>
											{m.common_tags()}
										</h3>
									</div>
									<Tags
										bind:tags={selectedTerm.tags}
										endpoint="/glossary"
										id={selectedTerm.id}
										canEdit={canEditTerm}
									/>
								</div>

								<!-- Metadata Section -->
								<div>
									<div class="flex items-center gap-2 mb-2">
										<Icon
											icon="material-symbols:database-outline"
											class="w-4 h-4 text-gray-500 dark:text-gray-400"
										/>
										<h3
											class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider"
										>
											{m.glossary_metadata_heading()}
										</h3>
									</div>
									{#if isEditing && editedTerm}
										<MetadataView
											bind:metadata={editedTerm.metadata}
											permissionResource="glossary"
											permissionAction="manage"
											readOnly={false}
											maxDepth={2}
											hidePaths={governedHidePaths}
											hasLeadingRows={detailFields.length > 0}
										>
											{#snippet leadingRows()}
												{#if metamodel && editedTerm}
													<ProductGovernedFields
														bind:metadata={editedTerm.metadata}
														productId={undefined}
														endpoint={`/glossary/${editedTerm.id}`}
														selfId={editedTerm.id}
														schema={metamodel}
														fields={detailFields}
														editable={canEditTerm}
													/>
												{/if}
											{/snippet}
										</MetadataView>
									{:else}
										<MetadataView
											bind:metadata={selectedTerm.metadata}
											endpoint="/glossary"
											id={selectedTerm.id}
											maxDepth={2}
											hidePaths={governedHidePaths}
											hasLeadingRows={detailFields.length > 0}
										>
											{#snippet leadingRows()}
												{#if metamodel && selectedTerm}
													<ProductGovernedFields
														bind:metadata={selectedTerm.metadata}
														productId={undefined}
														endpoint={`/glossary/${selectedTerm.id}`}
														selfId={selectedTerm.id}
														schema={metamodel}
														fields={detailFields}
														editable={canEditTerm}
													/>
												{/if}
											{/snippet}
										</MetadataView>
									{/if}
								</div>

								{#if metamodel?.enabled}
									<TermReferences
										termId={selectedTerm.id}
										schema={metamodel}
										onload={(count) => (referencingCount = count)}
									/>
								{/if}

								<EntityPanels entity={{ kind: 'glossary_term', id: selectedTerm.id }} withTabs />

								<!-- Description (Markdown Body) -->
								{#if selectedTerm.description || (isEditing && editedTerm)}
									<div>
										<div class="flex items-center gap-2 mb-2">
											<Icon
												icon="material-symbols:description-outline"
												class="w-4 h-4 text-gray-500 dark:text-gray-400"
											/>
											<h3
												class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider"
											>
												{m.common_description()}
											</h3>
										</div>
										{#if isEditing && editedTerm}
											<RichTextEditor
												bind:value={editedTerm.description}
												placeholder={m.glossary_description_edit_placeholder()}
											/>
										{:else if selectedTerm.description}
											<MarkdownRenderer content={selectedTerm.description} />
										{:else}
											<p class="text-sm text-gray-400 dark:text-gray-500 italic">
												{m.glossary_no_description()}
											</p>
										{/if}
									</div>
								{/if}

								<!-- Metadata -->
								<div>
									<div class="flex items-center gap-2 mb-2">
										<Icon
											icon="material-symbols:info-outline"
											class="w-4 h-4 text-gray-500 dark:text-gray-400"
										/>
										<h3
											class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider"
										>
											{m.common_details()}
										</h3>
									</div>
									<dl class="grid grid-cols-2 gap-4">
										<div>
											<dt class="text-xs text-gray-500 dark:text-gray-400">
												{m.glossary_created_label()}
											</dt>
											<dd class="text-sm text-gray-900 dark:text-gray-100 mt-0.5">
												{formatDate(selectedTerm.created_at)}
											</dd>
										</div>
										<div>
											<dt class="text-xs text-gray-500 dark:text-gray-400">
												{m.glossary_last_updated_label()}
											</dt>
											<dd class="text-sm text-gray-900 dark:text-gray-100 mt-0.5">
												{formatDate(selectedTerm.updated_at)}
											</dd>
										</div>
									</dl>
								</div>

								<!-- Actions -->
								{#if canEditTerm}
									<div
										class="flex items-center justify-end border-t border-gray-200 pt-5 dark:border-gray-700"
									>
										<button
											type="button"
											on:click={() => (showDeleteConfirm = true)}
											class="inline-flex items-center gap-2 rounded-lg border border-red-300 px-4 py-2 text-sm font-medium text-red-600 transition-colors hover:border-transparent hover:bg-red-600 hover:text-white dark:border-red-600 dark:text-red-400 dark:hover:bg-red-500"
										>
											<Icon icon="material-symbols:delete-outline" class="h-4 w-4" />
											{m.common_delete()}
										</button>
									</div>
								{/if}
							</div>
						</div>
					</div>
				{:else if $terms.length === 0}
					<div class="flex flex-col items-center justify-center py-16">
						<div
							class="w-20 h-20 rounded-2xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center mb-4"
						>
							<Icon
								icon="material-symbols:book-outline"
								class="text-4xl text-gray-400 dark:text-gray-500"
							/>
						</div>
						<h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-2">
							{m.glossary_empty_heading()}
						</h2>
						<p class="text-sm text-gray-500 dark:text-gray-400 text-center max-w-md mb-6">
							{m.glossary_empty_description()}
						</p>
						{#if canManageGlossary}
							<Button
								click={handleNewTerm}
								icon="material-symbols:add"
								text={m.glossary_create_first_button()}
								variant="filled"
							/>
						{/if}
					</div>
				{:else}
					<div class="flex items-center justify-center h-full">
						<div
							class="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-12 text-center max-w-md"
						>
							<Icon
								icon="material-symbols:book-outline"
								class="mx-auto h-12 w-12 text-gray-400 dark:text-gray-500 mb-4"
							/>
							<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-2">
								{m.glossary_no_term_selected_heading()}
							</h3>
							<p class="text-sm text-gray-500 dark:text-gray-400">
								{m.glossary_no_term_selected_hint()}
							</p>
						</div>
					</div>
				{/if}
			</div>
		</div>
	</div>
</div>

<!-- Create Modal -->
{#if showCreateModal}
	<div class="fixed inset-0 z-50 overflow-y-auto">
		<div class="flex items-center justify-center min-h-screen px-4">
			<div
				class="fixed inset-0 bg-black/50 dark:bg-black/70 backdrop-blur-sm transition-opacity"
				on:click={() => !isCreating && (showCreateModal = false)}
				on:keypress={(e) => e.key === 'Enter' && !isCreating && (showCreateModal = false)}
				role="button"
				tabindex="0"
			></div>

			<div
				class="relative bg-white/95 dark:bg-gray-800/95 backdrop-blur-md rounded-xl shadow-2xl max-w-2xl w-full p-6 z-10 border border-gray-200/50 dark:border-gray-700/50"
			>
				<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-4">
					{m.glossary_create_modal_heading()}
				</h3>

				{#if createError}
					<div class="mb-4 rounded-lg bg-red-50 dark:bg-red-900/20 p-3">
						<div class="flex">
							<Icon icon="material-symbols:error" class="h-5 w-5 text-red-400" />
							<div class="ml-3">
								<p class="text-sm text-red-800 dark:text-red-200">{createError}</p>
							</div>
						</div>
					</div>
				{/if}

				<form on:submit|preventDefault={createTerm} class="space-y-4">
					<div>
						<label
							for="term-name"
							class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1"
						>
							{m.common_name()} <span class="text-red-500">*</span>
						</label>
						<input
							id="term-name"
							type="text"
							bind:value={newTermName}
							disabled={isCreating}
							placeholder={m.glossary_name_placeholder()}
							class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md shadow-sm focus:ring-earthy-terracotta-600 focus:border-earthy-terracotta-700 dark:bg-gray-700 dark:text-gray-100 disabled:opacity-50"
							required
						/>
					</div>

					<div>
						<label
							for="term-definition"
							class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1"
						>
							{m.glossary_definition_label()} <span class="text-red-500">*</span>
						</label>
						<textarea
							id="term-definition"
							bind:value={newTermDefinition}
							disabled={isCreating}
							placeholder={m.glossary_definition_placeholder()}
							rows="3"
							class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md shadow-sm focus:ring-earthy-terracotta-600 focus:border-earthy-terracotta-700 dark:bg-gray-700 dark:text-gray-100 disabled:opacity-50"
							required
						></textarea>
					</div>

					<div>
						<label
							for="term-description"
							class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1"
						>
							{m.glossary_description_optional_label()}
						</label>
						<RichTextEditor
							bind:value={newTermDescription}
							disabled={isCreating}
							placeholder={m.glossary_description_placeholder()}
						/>
					</div>

					<div>
						<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
							{m.glossary_owners_optional_label()}
						</label>
						<OwnerSelector
							bind:selectedOwners={newTermOwners}
							onChange={(owners) => {
								newTermOwners = owners;
							}}
							placeholder={m.glossary_owners_placeholder()}
						/>
						<p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
							{m.glossary_owners_default_hint()}
						</p>
					</div>

					<DomainSelect id="term-domain" bind:value={newTermDomain} />

					<div class="flex justify-end gap-3 pt-4">
						<Button
							type="button"
							click={() => (showCreateModal = false)}
							disabled={isCreating}
							text={m.common_cancel()}
							variant="clear"
						/>
						<Button
							type="submit"
							disabled={isCreating}
							loading={isCreating}
							text={isCreating ? m.glossary_creating() : m.glossary_create_term_button()}
							variant="filled"
						/>
					</div>
				</form>
			</div>
		</div>
	</div>
{/if}

<!-- Delete Confirm Modal -->
{#if showDeleteConfirm}
	<div class="fixed inset-0 z-50 overflow-y-auto">
		<div class="flex items-center justify-center min-h-screen px-4">
			<div
				class="fixed inset-0 bg-black/50 dark:bg-black/70 backdrop-blur-sm transition-opacity"
				on:click={() => (showDeleteConfirm = false)}
				on:keypress={(e) => e.key === 'Enter' && (showDeleteConfirm = false)}
				role="button"
				tabindex="0"
			></div>

			<div
				class="relative bg-white/95 dark:bg-gray-800/95 backdrop-blur-md rounded-xl shadow-2xl max-w-lg w-full p-6 z-10 border border-gray-200/50 dark:border-gray-700/50"
			>
				<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-2">
					{m.glossary_delete_modal_heading()}
				</h3>
				<p class="text-sm text-gray-500 dark:text-gray-400 mb-4">
					{m.glossary_delete_confirm({ name: selectedTerm?.name ?? '' })}
				</p>
				{#if referencingCount > 0}
					<p
						class="mb-4 rounded-md bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
					>
						{m.glossary_delete_referenced({ count: referencingCount })}
					</p>
				{/if}
				<div class="flex justify-end gap-3">
					<button
						on:click={() => (showDeleteConfirm = false)}
						class="px-4 py-2 border border-gray-300 dark:border-gray-600 rounded-md text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700"
					>
						{m.common_cancel()}
					</button>
					<button
						on:click={deleteTerm}
						class="px-4 py-2 rounded-md text-sm font-medium text-white bg-red-600 hover:bg-red-700 dark:bg-red-500 dark:hover:bg-red-600"
					>
						{m.common_delete()}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
