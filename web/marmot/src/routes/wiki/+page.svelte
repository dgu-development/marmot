<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve -- link() resolves the wiki route; evidence URLs come from the catalog API. */
	import { onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { fetchApi } from '$lib/api';
	import { m } from '$lib/paraglide/messages';
	import MarkdownRenderer from '$components/ui/MarkdownRenderer.svelte';
	import MemoryPanel from '$components/memory/MemoryPanel.svelte';
	import Icon from '@iconify/svelte';

	type Source = {
		id: string;
		title: string;
		url: string;
		hash: string;
		field: string;
		preview?: string;
	};
	type WikiPage = {
		entity_type: string;
		entity_id: string;
		title: string;
		entity_url: string;
		content: string;
		draft_content: string;
		draft_hash: string;
		status: string;
		freshness: string;
		draft_freshness: string;
		mode: string;
		draft_mode: string;
		published_sources: Source[];
		draft_sources: Source[];
		error?: string;
	};
	type Index = { pages: WikiPage[]; total: number };
	type Settings = { can_write: boolean; mode: string; interval_seconds: number };
	type Batch = {
		compiled: number;
		skipped: number;
		next_offset: number;
		total: number;
		errors: { entity_type: string; entity_id: string; error: string }[];
	};
	let index = $state<Index>({ pages: [], total: 0 });
	let settings = $state<Settings>({ can_write: false, mode: 'extractive', interval_seconds: 0 });
	let selected = $state<WikiPage | null>(null);
	let query = $state('');
	let loading = $state(true);
	let pageLoading = $state(false);
	let error = $state('');
	let notice = $state('');
	let busy = $state(false);
	let compiling = $state(false);
	let draft = $state(false);
	let libraryOpen = $state(false);
	let progress = $state({ done: 0, total: 0, failed: 0 });
	let failures = $state<Batch['errors']>([]);
	let batchController: AbortController | undefined;
	let selectedSequence = 0;
	let listSequence = 0;
	let offset = $derived(Math.max(0, Number($page.url.searchParams.get('offset')) || 0));
	let search = $derived($page.url.searchParams.get('q') || '');
	let kind = $derived($page.url.searchParams.get('entity_type') || '');
	let id = $derived($page.url.searchParams.get('entity_id') || '');
	let body = $derived(selected ? (draft ? selected.draft_content : selected.content) : '');
	let sources = $derived((draft ? selected?.draft_sources : selected?.published_sources) || []);
	let stale = $derived((draft ? selected?.draft_freshness : selected?.freshness) === 'stale');
	let viewStatus = $derived(draft ? 'draft' : selected?.status || 'uncompiled');
	let activeMode = $derived(draft ? selected?.draft_mode : selected?.mode);
	let leadSource = $derived(
		sources.find((source) =>
			['user_description', 'description', 'user_definition', 'definition'].includes(source.field)
		)
	);
	let bodySources = $derived(sources.filter((source) => source.id !== leadSource?.id));
	let sourceGroups = $derived([
		{
			id: 'catalog',
			title: m.wiki_group_catalog(),
			icon: 'material-symbols:database-outline-rounded'
		},
		{
			id: 'docs',
			title: m.wiki_group_docs(),
			icon: 'material-symbols:description-outline-rounded'
		},
		{
			id: 'memory',
			title: m.wiki_group_memory(),
			icon: 'material-symbols:lightbulb-outline-rounded'
		},
		{
			id: 'relations',
			title: m.wiki_group_relations(),
			icon: 'material-symbols:account-tree-outline-rounded'
		},
		{ id: 'model', title: m.wiki_group_model(), icon: 'material-symbols:schema-outline-rounded' }
	]);
	const size = 20;

	function label(value: string): string {
		const labels: Record<string, () => string> = {
			asset: m.wiki_asset,
			data_product: m.wiki_data_product,
			glossary_term: m.wiki_glossary_term,
			domain: m.wiki_domain,
			uncompiled: m.wiki_uncompiled,
			draft: m.wiki_draft,
			published: m.wiki_published,
			fresh: m.wiki_fresh,
			stale: m.wiki_stale,
			unverified: m.wiki_unverified
		};
		return labels[value]?.() || value;
	}
	function groupOf(source: Source) {
		if (source.id.startsWith('memory:')) return 'memory';
		if (/^(doc:|imported-doc:|global-doc:)/.test(source.id)) return 'docs';
		if (/^(relation:|metamodel-link:)/.test(source.id)) return 'relations';
		if (source.id.startsWith('metamodel:')) return 'model';
		return 'catalog';
	}
	function kindIcon(value: string) {
		return (
			{
				asset: 'material-symbols:database-outline-rounded',
				data_product: 'material-symbols:deployed-code-outline',
				glossary_term: 'material-symbols:menu-book-outline-rounded',
				domain: 'material-symbols:category-outline-rounded'
			}[value] || 'material-symbols:article-outline-rounded'
		);
	}
	async function request<T>(
		path: string,
		options: Parameters<typeof fetchApi>[1] = {}
	): Promise<T> {
		const response = await fetchApi(`/knowledge${path}`, options);
		if (!response.ok)
			throw new Error(
				response.status === 409
					? m.wiki_conflict()
					: response.status === 403
						? m.wiki_denied()
						: m.wiki_error()
			);
		return response.json();
	}
	function message(e: unknown) {
		return e instanceof Error ? e.message : m.wiki_error();
	}
	function entityPath(entity: WikiPage) {
		return `/pages/${entity.entity_type}/${encodeURIComponent(entity.entity_id)}`;
	}
	function link(
		entity?: { entity_type: string; entity_id: string },
		newOffset = offset,
		newSearch = search
	) {
		const params: [string, string][] = [
			['q', newSearch],
			['offset', String(newOffset)]
		];
		if (entity) params.push(['entity_type', entity.entity_type], ['entity_id', entity.entity_id]);
		return `${resolve('/wiki')}?${new URLSearchParams(params)}`;
	}
	async function loadList(q: string, start: number) {
		const seq = ++listSequence;
		loading = true;
		try {
			const result = await request<Index>(
				`?${new URLSearchParams({ q, offset: String(start), limit: String(size) })}`
			);
			if (seq === listSequence) {
				index = result;
				if (!kind && !id && !q && result.pages.length)
					void goto(link(result.pages[0], start, q), { replaceState: true });
			}
		} catch (e) {
			if (seq === listSequence) error = message(e);
		} finally {
			if (seq === listSequence) loading = false;
		}
	}
	async function loadPage(entityType: string, entityId: string) {
		const seq = ++selectedSequence;
		selected = null;
		if (!entityType || !entityId) {
			pageLoading = false;
			return;
		}
		pageLoading = true;
		try {
			const result = await request<WikiPage>(
				`/pages/${encodeURIComponent(entityType)}/${encodeURIComponent(entityId)}`
			);
			if (seq === selectedSequence) {
				selected = result;
				draft = result.status !== 'published' && settings.can_write;
			}
		} catch (e) {
			if (seq === selectedSequence) error = message(e);
		} finally {
			if (seq === selectedSequence) pageLoading = false;
		}
	}
	$effect(() => {
		query = search;
		void loadList(search, offset);
	});
	$effect(() => {
		void loadPage(kind, id);
	});
	$effect(() => {
		void request<Settings>('/settings')
			.then((result) => {
				settings = result;
				if (selected?.status === 'draft') draft = result.can_write;
			})
			.catch((e) => {
				error = message(e);
			});
	});
	onDestroy(() => {
		batchController?.abort();
		selectedSequence++;
		listSequence++;
	});

	async function mutate(action: 'compile' | 'publish') {
		if (!selected) return;
		const current = selected;
		busy = true;
		error = '';
		notice = '';
		try {
			const updated = await request<WikiPage>(`${entityPath(current)}/${action}`, {
				method: 'POST',
				body: action === 'publish' ? JSON.stringify({ draft_hash: current.draft_hash }) : undefined
			});
			if (id === current.entity_id && kind === current.entity_type) {
				selected = updated;
				draft = action === 'compile';
			}
			if (action === 'publish') notice = m.wiki_saved();
			await loadList(search, offset);
		} catch (e) {
			error = message(e);
		} finally {
			busy = false;
		}
	}
	async function compileAll() {
		compiling = true;
		error = '';
		notice = '';
		failures = [];
		progress = { done: 0, total: index.total, failed: 0 };
		batchController = new AbortController();
		try {
			let next = 0;
			do {
				const result = await request<Batch>(`/compile?limit=1&offset=${next}`, {
					method: 'POST',
					signal: batchController.signal
				});
				failures = [...failures, ...result.errors];
				progress = {
					done: progress.done + result.compiled + result.skipped + result.errors.length,
					total: result.total,
					failed: failures.length
				};
				next = result.next_offset;
			} while (next > 0 && !batchController.signal.aborted);
			notice = failures.length ? m.wiki_compile_errors() : m.wiki_complete();
		} catch (e) {
			if (!batchController.signal.aborted) error = message(e);
		} finally {
			compiling = false;
			await loadList(search, offset);
			await loadPage(kind, id);
		}
	}
</script>

<svelte:head><title>{m.wiki_title()}</title></svelte:head>

<div class="min-h-full bg-[#f8f7f4] text-[#252522] dark:bg-gray-950 dark:text-gray-100">
	<div class="mx-auto max-w-[1600px] px-4 sm:px-7 xl:px-10">
		<header
			class="flex flex-wrap items-center justify-between gap-4 border-b border-[#e9e5de] py-5 dark:border-gray-800"
		>
			<div class="flex items-center gap-3">
				<span
					class="flex h-9 w-9 items-center justify-center rounded-lg bg-earthy-terracotta-700 text-white"
					><Icon icon="material-symbols:book-2-outline-rounded" class="h-5 w-5" /></span
				>
				<h1 class="text-lg font-semibold tracking-tight">{m.wiki_title()}</h1>
			</div>
			<div class="flex items-center gap-2">
				<button
					class="wiki-action"
					onclick={() => {
						error = '';
						void loadList(search, offset);
						void loadPage(kind, id);
					}}
					disabled={busy}
					title={m.wiki_refresh()}
					aria-label={m.wiki_refresh()}
					><Icon icon="material-symbols:refresh-rounded" class="h-[18px] w-[18px]" /></button
				>{#if settings.can_write}{#if compiling}<button
							class="wiki-action"
							onclick={() => batchController?.abort()}
							><Icon
								icon="material-symbols:stop-circle-outline-rounded"
								class="h-4 w-4"
							/>{m.wiki_stop()}</button
						>{:else}<button
							class="wiki-action wiki-action-primary"
							onclick={compileAll}
							disabled={busy || index.total === 0}
							><Icon
								icon="material-symbols:play-arrow-rounded"
								class="h-4 w-4"
							/>{m.wiki_compile_all()}</button
						>{/if}{/if}
			</div>
		</header>

		{#if error}<div
				role="alert"
				class="mt-4 border-l-2 border-red-500 bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-950 dark:text-red-200"
			>
				{error}
			</div>{/if}
		{#if notice}<div
				role="status"
				class="mt-4 border-l-2 border-emerald-500 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200"
			>
				{notice}
			</div>{/if}
		{#if compiling || progress.done > 0}<div
				role="status"
				class="mt-4 flex flex-wrap items-center gap-4 border-b border-gray-200 pb-4 text-xs text-gray-600 dark:border-gray-800 dark:text-gray-300"
			>
				<span
					>{m.wiki_progress({
						done: String(progress.done),
						total: String(progress.total),
						failed: String(progress.failed)
					})}</span
				><progress
					class="h-1.5 min-w-40 flex-1 accent-earthy-terracotta-700"
					value={progress.done}
					max={Math.max(1, progress.total)}
					aria-label={m.wiki_compiling()}
				></progress>
			</div>{/if}
		{#if failures.length}<ul class="mt-3 flex flex-wrap gap-3 text-xs text-red-700">
				{#each failures as failure (`${failure.entity_type}:${failure.entity_id}`)}<li>
						<a class="underline" href={link(failure)}
							>{label(failure.entity_type)} · {failure.entity_id}</a
						>
					</li>{/each}
			</ul>{/if}

		<div
			class="grid min-h-[calc(100vh-9rem)] items-start lg:grid-cols-[252px_minmax(0,1fr)] xl:grid-cols-[252px_minmax(0,1fr)_208px]"
		>
			<aside
				class="border-b border-[#e9e5de] py-5 dark:border-gray-800 lg:sticky lg:top-16 lg:min-h-[calc(100vh-9rem)] lg:border-b-0 lg:border-r lg:pr-5"
				aria-label={m.wiki_library()}
			>
				<button
					class="flex w-full items-center justify-between text-left lg:pointer-events-none"
					onclick={() => (libraryOpen = !libraryOpen)}
					aria-expanded={libraryOpen || !selected}
				>
					<span
						class="text-[11px] font-semibold uppercase tracking-[0.15em] text-gray-500 dark:text-gray-400"
						>{m.wiki_library()}
						<span class="ml-1 font-normal tabular-nums tracking-normal text-gray-400"
							>{index.total}</span
						></span
					><Icon
						icon={libraryOpen
							? 'material-symbols:expand-less-rounded'
							: 'material-symbols:expand-more-rounded'}
						class="h-5 w-5 text-gray-400 lg:hidden"
					/>
				</button>
				<div class={selected && !libraryOpen ? 'hidden lg:block' : ''}>
					<form
						class="relative mt-5"
						onsubmit={(e) => {
							e.preventDefault();
							void goto(link(undefined, 0, query));
						}}
					>
						<label class="sr-only" for="wiki-search">{m.wiki_search()}</label><Icon
							icon="material-symbols:search-rounded"
							class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400"
						/><input
							id="wiki-search"
							type="search"
							bind:value={query}
							placeholder={m.wiki_search()}
							class="w-full rounded-md border border-[#e9e5de] bg-white py-2.5 pl-9 pr-3 text-sm outline-none transition placeholder:text-gray-400 focus:border-earthy-terracotta-500 focus:ring-2 focus:ring-earthy-terracotta-100 dark:border-gray-700 dark:bg-gray-900"
						/><button type="submit" class="sr-only">{m.wiki_search_action()}</button>
					</form>
					<nav
						aria-label={m.wiki_title()}
						aria-busy={loading}
						class="mt-6 max-h-[50vh] overflow-y-auto lg:max-h-[calc(100vh-19rem)]"
					>
						{#if loading}<p class="py-4 text-xs text-gray-500">{m.wiki_loading()}</p>{/if}
						{#each ['asset', 'data_product', 'glossary_term', 'domain'] as entityKind (entityKind)}
							{@const entries = index.pages.filter((entry) => entry.entity_type === entityKind)}
							{#if entries.length}<section class="mb-5">
									<h2
										class="mb-2 px-2 text-[10px] font-semibold uppercase tracking-[0.13em] text-gray-400"
									>
										{label(entityKind)}
									</h2>
									{#each entries as entry (`${entry.entity_type}:${entry.entity_id}`)}<a
											href={link(entry)}
											onclick={() => (libraryOpen = false)}
											aria-current={kind === entry.entity_type && id === entry.entity_id
												? 'page'
												: undefined}
											class="group flex items-center gap-2 border-l-2 py-2 pl-2 pr-1 text-sm transition {kind ===
												entry.entity_type && id === entry.entity_id
												? 'border-earthy-terracotta-600 bg-earthy-terracotta-50/70 font-semibold text-earthy-terracotta-900 dark:bg-earthy-terracotta-900/20 dark:text-earthy-terracotta-100'
												: 'border-transparent text-gray-600 hover:bg-white hover:text-gray-900 dark:text-gray-300 dark:hover:bg-gray-900'}"
											><Icon
												icon={kindIcon(entry.entity_type)}
												class="h-4 w-4 shrink-0 text-gray-400 group-aria-[current=page]:text-earthy-terracotta-600"
											/><span class="min-w-0 flex-1 truncate">{entry.title}</span><span
												class="h-1.5 w-1.5 shrink-0 rounded-full {entry.freshness === 'stale' ||
												entry.draft_freshness === 'stale'
													? 'bg-amber-500'
													: entry.status === 'published'
														? 'bg-emerald-500'
														: entry.status === 'draft'
															? 'bg-earthy-terracotta-500'
															: 'bg-gray-300'}"
												title={label(entry.status)}
											></span></a
										>{/each}
								</section>{/if}
						{:else}{#if !loading}<p class="py-6 text-xs leading-5 text-gray-500">
									{m.wiki_empty()}
								</p>{/if}{/each}
					</nav>
					<div
						class="flex items-center justify-between border-t border-[#e9e5de] pt-3 text-[11px] tabular-nums text-gray-500 dark:border-gray-800"
					>
						<span
							>{index.total
								? `${offset + 1}–${Math.min(offset + size, index.total)} / ${index.total}`
								: '0'}</span
						>
						<div class="flex gap-1">
							<button
								class="p-1.5 hover:text-earthy-terracotta-700 disabled:opacity-30"
								disabled={offset === 0 || loading}
								onclick={() => goto(link(selected ?? undefined, Math.max(0, offset - size)))}
								aria-label={m.wiki_previous()}
								><Icon icon="material-symbols:arrow-back-rounded" class="h-4 w-4" /></button
							><button
								class="p-1.5 hover:text-earthy-terracotta-700 disabled:opacity-30"
								disabled={offset + size >= index.total || loading}
								onclick={() => goto(link(selected ?? undefined, offset + size))}
								aria-label={m.wiki_next()}
								><Icon icon="material-symbols:arrow-forward-rounded" class="h-4 w-4" /></button
							>
						</div>
					</div>
					<p
						class="mt-5 border-t border-[#e9e5de] pt-4 text-[11px] leading-5 text-gray-400 dark:border-gray-800"
					>
						{settings.mode === 'llm' ? m.wiki_llm() : m.wiki_extractive()} · {settings.interval_seconds
							? m.wiki_automatic({ minutes: String(Math.round(settings.interval_seconds / 60)) })
							: m.wiki_manual()}
					</p>
				</div>
			</aside>

			<main aria-busy={pageLoading} class="min-w-0 bg-white dark:bg-gray-900">
				{#if pageLoading}<div class="mx-auto max-w-[850px] animate-pulse px-6 py-12 sm:px-10">
						<div class="mb-7 h-3 w-36 bg-gray-100 dark:bg-gray-800"></div>
						<div class="mb-5 h-10 w-2/3 bg-gray-100 dark:bg-gray-800"></div>
						<div class="h-4 w-full bg-gray-100 dark:bg-gray-800"></div>
					</div>
				{:else if selected}
					<article class="mx-auto max-w-[850px] px-6 pb-20 pt-9 sm:px-10 sm:pt-12 xl:px-14">
						<div
							class="mb-7 flex flex-wrap items-center justify-between gap-3 text-[11px] font-medium text-gray-500"
						>
							<span class="flex items-center gap-2"
								><span class="text-earthy-terracotta-700 dark:text-earthy-terracotta-400"
									>{m.wiki_title()}</span
								><Icon
									icon="material-symbols:chevron-right-rounded"
									class="h-3 w-3 text-gray-300"
								/>{label(selected.entity_type)}</span
							><a
								href={selected.entity_url}
								class="inline-flex items-center gap-1 text-gray-500 transition hover:text-earthy-terracotta-700 dark:hover:text-earthy-terracotta-400"
								>{m.wiki_entity()}<Icon
									icon="material-symbols:arrow-outward-rounded"
									class="h-3.5 w-3.5"
								/></a
							>
						</div>
						<header id="wiki-overview" class="scroll-mt-24">
							<div class="mb-4 flex items-center gap-2">
								<span
									class="h-1.5 w-1.5 rounded-full {stale
										? 'bg-amber-500'
										: viewStatus === 'published'
											? 'bg-emerald-500'
											: 'bg-gray-400'}"
								></span><span
									class="text-[11px] font-medium uppercase tracking-[0.13em] text-gray-500"
									>{stale ? m.wiki_stale() : label(viewStatus)}</span
								><span class="text-gray-300">/</span><span class="text-[11px] text-gray-500"
									>{m.wiki_source_count({ total: String(sources.length) })}</span
								>
							</div>
							<h2
								class="break-words text-[2.35rem] font-semibold leading-[1.12] tracking-tight sm:text-[3rem]"
							>
								{selected.title}
							</h2>
							<div class="mt-6 h-0.5 w-10 bg-earthy-terracotta-600"></div>
						</header>
						<div
							class="mt-7 flex flex-wrap items-center justify-between gap-3 border-b border-[#e9e5de] dark:border-gray-800"
						>
							<div class="flex gap-6">
								<button
									class="border-b-2 pb-3 text-xs font-semibold transition {!draft
										? 'border-earthy-terracotta-700 text-earthy-terracotta-700 dark:text-earthy-terracotta-400'
										: 'border-transparent text-gray-500 hover:text-gray-900'}"
									aria-pressed={!draft}
									onclick={() => (draft = false)}>{m.wiki_published_view()}</button
								>{#if settings.can_write}<button
										class="border-b-2 pb-3 text-xs font-semibold transition {draft
											? 'border-earthy-terracotta-700 text-earthy-terracotta-700 dark:text-earthy-terracotta-400'
											: 'border-transparent text-gray-500 hover:text-gray-900'}"
										aria-pressed={draft}
										onclick={() => (draft = true)}>{m.wiki_draft_view()}</button
									>{/if}
							</div>
							{#if settings.can_write}<div class="flex flex-wrap items-center gap-3 pb-2">
									<button
										class="inline-flex items-center gap-1 text-xs font-medium text-gray-500 transition hover:text-earthy-terracotta-700 disabled:opacity-50"
										disabled={busy || compiling}
										onclick={() => mutate('compile')}
										><Icon icon="material-symbols:refresh-rounded" class="h-4 w-4" />{busy
											? m.wiki_compiling()
											: m.wiki_compile()}</button
									>{#if draft && selected.draft_hash && selected.draft_freshness === 'fresh'}<button
											class="wiki-action wiki-action-primary"
											disabled={busy || compiling}
											onclick={() => mutate('publish')}
											>{m.wiki_publish()}<Icon
												icon="material-symbols:arrow-forward-rounded"
												class="h-4 w-4"
											/></button
										>{/if}
								</div>{/if}
						</div>
						{#if draft}<p
								class="mt-6 border-l-2 border-earthy-terracotta-500 pl-4 text-xs leading-6 text-gray-600 dark:text-gray-300"
							>
								{m.wiki_review_help()}
							</p>{/if}
						{#if stale}<div
								role="status"
								class="mt-10 border-l-2 border-amber-500 bg-amber-50 px-5 py-4 text-sm leading-6 text-amber-900 dark:bg-amber-950 dark:text-amber-200"
							>
								{m.wiki_stale_help()}
							</div>
						{:else if body}
							{#if activeMode === 'extractive'}
								<div class="mt-10">
									<p
										class="mb-3 text-[10px] font-semibold uppercase tracking-[0.17em] text-earthy-terracotta-700 dark:text-earthy-terracotta-400"
									>
										{m.wiki_extractive()}
									</p>
								</div>
								{#if leadSource}<div class="mt-7 border-l-2 border-[#e2c1af] pl-5">
										<p
											class="break-words text-lg font-medium leading-relaxed text-gray-800 dark:text-gray-200"
										>
											{leadSource.preview}
										</p>
										<a
											href={leadSource.url}
											class="mt-3 inline-flex items-center gap-1 text-xs font-medium text-earthy-terracotta-700 hover:underline dark:text-earthy-terracotta-400"
											>{m.wiki_open_source()}<Icon
												icon="material-symbols:arrow-outward-rounded"
												class="h-3.5 w-3.5"
											/></a
										>
									</div>{/if}
								{#each sourceGroups as group (group.id)}{@const entries = bodySources.filter(
										(source) => groupOf(source) === group.id
									)}{#if entries.length}<section
											id={`wiki-group-${group.id}`}
											class="mt-12 scroll-mt-24"
										>
											<div
												class="flex items-baseline justify-between border-b border-[#dedad3] pb-3 dark:border-gray-700"
											>
												<h3 class="text-lg font-semibold tracking-tight">{group.title}</h3>
												<span class="text-xs tabular-nums text-gray-400"
													>{String(entries.length).padStart(2, '0')}</span
												>
											</div>
											{#if group.id === 'catalog'}<dl>
													{#each entries as source (source.id)}<div
															class="flex gap-4 border-b border-[#efede8] py-4 dark:border-gray-800"
														>
															<dt
																class="w-28 shrink-0 break-words text-xs font-medium text-gray-500 sm:w-36"
															>
																{source.title}
															</dt>
															<dd
																class="min-w-0 flex-1 break-words text-sm leading-6 text-gray-800 dark:text-gray-200"
															>
																{source.preview || source.field}
															</dd>
															<a
																href={source.url}
																title={m.wiki_open_source()}
																aria-label={`${m.wiki_open_source()}: ${source.title}`}
																class="shrink-0 self-start text-gray-400 transition hover:text-earthy-terracotta-700"
																><Icon
																	icon="material-symbols:arrow-outward-rounded"
																	class="h-4 w-4"
																/></a
															>
														</div>{/each}
												</dl>{:else}<div class="divide-y divide-[#efede8] dark:divide-gray-800">
													{#each entries as source (source.id)}<div class="py-5">
															<div class="flex items-center justify-between gap-3">
																<h4 class="break-words text-sm font-semibold">{source.title}</h4>
																<a
																	href={source.url}
																	class="shrink-0 text-xs text-earthy-terracotta-700 hover:underline dark:text-earthy-terracotta-400"
																	>{m.wiki_open_source()} ↗</a
																>
															</div>
															{#if source.preview && group.id !== 'model'}<p
																	class="mt-2 break-words text-sm leading-6 text-gray-600 dark:text-gray-400"
																>
																	{source.preview}
																</p>{/if}
															<p class="mt-2 text-[11px] text-gray-400">{source.field}</p>
														</div>{/each}
												</div>{/if}
										</section>{/if}{/each}
								<details class="mt-12 border-t border-[#e9e5de] pt-4 text-xs dark:border-gray-800">
									<summary
										class="flex cursor-pointer list-none items-center gap-2 font-medium text-gray-500 marker:hidden hover:text-earthy-terracotta-700"
										><Icon
											icon="material-symbols:code-blocks-outline-rounded"
											class="h-4 w-4"
										/>{m.wiki_full_document()}<Icon
											icon="material-symbols:expand-more-rounded"
											class="ml-auto h-4 w-4"
										/></summary
									>
									<div
										class="wiki-document mt-5 overflow-x-auto border-t border-[#e9e5de] pt-5 dark:border-gray-800"
									>
										<MarkdownRenderer content={body} />
									</div>
								</details>
							{:else}<div class="wiki-document mt-10"><MarkdownRenderer content={body} /></div>
								<section class="mt-12 border-t border-[#e9e5de] pt-6 dark:border-gray-800">
									<h3 class="text-lg font-semibold tracking-tight">{m.wiki_sources()}</h3>
									<p class="mt-1 text-xs text-gray-500">{m.wiki_sources_help()}</p>
									<div class="mt-5 divide-y divide-[#efede8] dark:divide-gray-800">
										{#each sources as source (source.id)}<a
												href={source.url}
												class="flex items-center justify-between gap-4 py-3 text-sm hover:text-earthy-terracotta-700"
												><span class="min-w-0 truncate">{source.title}</span><Icon
													icon="material-symbols:arrow-outward-rounded"
													class="h-4 w-4 shrink-0"
												/></a
											>{/each}
									</div>
								</section>{/if}
						{:else}<div class="mt-10 border-l-2 border-[#e9e5de] py-2 pl-5 dark:border-gray-700">
								<h3 class="text-lg font-semibold tracking-tight">
									{draft ? m.wiki_draft_view() : m.wiki_published_view()}
								</h3>
								<p class="mt-2 text-sm leading-6 text-gray-500">
									{draft ? m.wiki_no_draft() : m.wiki_no_page()}
								</p>
							</div>{/if}
						{#if selected.entity_type === 'asset' || selected.entity_type === 'data_product'}<details
								class="mt-14 border-t border-[#e9e5de] pt-6 dark:border-gray-800"
							>
								<summary class="flex cursor-pointer list-none items-center gap-3 marker:hidden"
									><span
										class="flex h-8 w-8 items-center justify-center rounded-full bg-earthy-terracotta-50 text-earthy-terracotta-700 dark:bg-earthy-terracotta-900/30"
										><Icon
											icon="material-symbols:lightbulb-outline-rounded"
											class="h-4 w-4"
										/></span
									><span class="min-w-0 flex-1"
										><span class="block text-sm font-semibold">{m.wiki_memory()}</span><span
											class="mt-1 block text-xs text-gray-500">{m.wiki_memory_help()}</span
										></span
									><Icon
										icon="material-symbols:add-rounded"
										class="h-4 w-4 text-gray-500"
									/></summary
								>
								<div class="pt-6">
									{#key `${selected.entity_type}:${selected.entity_id}`}<MemoryPanel
											entityType={selected.entity_type}
											entityId={selected.entity_id}
										/>{/key}
								</div>
							</details>{/if}
					</article>
				{:else}<div class="mx-auto max-w-[850px] px-6 py-24 text-center sm:px-10">
						<Icon
							icon="material-symbols:auto-stories-outline-rounded"
							class="mx-auto h-9 w-9 text-earthy-terracotta-600"
						/>
						<h2 class="mt-6 text-3xl font-semibold tracking-tight">{m.wiki_select()}</h2>
						<p class="mx-auto mt-3 max-w-sm text-sm leading-6 text-gray-500">
							{m.wiki_select_help()}
						</p>
					</div>{/if}
			</main>

			<aside
				class="hidden border-l border-[#e9e5de] pl-5 pt-12 dark:border-gray-800 xl:sticky xl:top-16 xl:block"
				aria-label={m.wiki_contents()}
			>
				{#if selected}<p
						class="mb-5 text-[10px] font-semibold uppercase tracking-[0.16em] text-gray-400"
					>
						{activeMode === 'extractive' ? m.wiki_contents() : m.wiki_sources()}
					</p>
					<nav class="space-y-1 border-l border-[#dedad3] dark:border-gray-700">
						{#if activeMode === 'extractive'}<a
								href="#wiki-overview"
								class="block -ml-px border-l border-earthy-terracotta-600 py-1.5 pl-3 text-xs font-medium text-earthy-terracotta-700 dark:text-earthy-terracotta-400"
								>{m.wiki_title()}</a
							>{#each sourceGroups as group (group.id)}{@const count = bodySources.filter(
									(source) => groupOf(source) === group.id
								).length}{#if count}<a
										href={`#wiki-group-${group.id}`}
										class="flex items-center justify-between gap-2 py-1.5 pl-3 text-xs text-gray-500 transition hover:text-earthy-terracotta-700"
										><span class="truncate">{group.title}</span><span
											class="tabular-nums text-gray-400">{count}</span
										></a
									>{/if}{/each}{:else}{#each sources.slice(0, 12) as source (source.id)}<a
									href={source.url}
									class="block truncate py-1.5 pl-3 text-xs text-gray-500 transition hover:text-earthy-terracotta-700"
									>{source.title}</a
								>{/each}{/if}
					</nav>
					<p
						class="mt-9 border-t border-[#e9e5de] pt-5 text-[11px] leading-5 text-gray-400 dark:border-gray-800"
					>
						{m.wiki_sources_help()}
					</p>{/if}
			</aside>
		</div>
	</div>
</div>

<style>
	.wiki-action {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 0.45rem;
		min-height: 2.25rem;
		border: 1px solid #e9e5de;
		border-radius: 0.4rem;
		padding: 0.4rem 0.7rem;
		color: #57534e;
		background: white;
		font-size: 0.75rem;
		font-weight: 600;
		transition:
			background 0.15s,
			border-color 0.15s;
	}
	.wiki-action:hover {
		border-color: #d2c9bd;
		background: #f7f3ef;
	}
	.wiki-action:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.wiki-action:focus-visible {
		outline: 2px solid #b45837;
		outline-offset: 2px;
	}
	.wiki-action-primary {
		border-color: #a94728;
		background: #a94728;
		color: white;
	}
	.wiki-action-primary:hover {
		border-color: #8c371d;
		background: #8c371d;
	}
	:global(.dark) .wiki-action {
		border-color: #374151;
		color: #e5e7eb;
		background: #1f2937;
	}
	:global(.dark) .wiki-action-primary {
		border-color: #b45837;
		background: #b45837;
		color: white;
	}
	.wiki-document :global(.prose) {
		font-size: 0.95rem;
		line-height: 1.8;
	}
	.wiki-document :global(.prose h1:first-child) {
		display: none;
	}
	.wiki-document :global(.prose pre) {
		border: 1px solid #e9e5de;
	}
</style>
