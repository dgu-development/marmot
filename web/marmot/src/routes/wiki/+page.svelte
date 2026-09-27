<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve -- link() resolves the wiki route; evidence URLs come from the catalog API. */
	import { onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { fetchApi } from '$lib/api';
	import { m } from '$lib/paraglide/messages';
	import WikiArticle from '$components/knowledge/WikiArticle.svelte';
	import type { WikiPage, WikiSettings } from '$lib/knowledge/types';
	import Icon from '@iconify/svelte';

	type Index = { pages: WikiPage[]; total: number };
	let index = $state<Index>({ pages: [], total: 0 });
	let settings = $state<WikiSettings>({
		can_write: false,
		mode: 'extractive',
		interval_seconds: 0
	});
	let selected = $state<WikiPage | null>(null);
	let query = $state('');
	let loading = $state(true);
	let pageLoading = $state(false);
	let error = $state('');
	let notice = $state('');
	let busy = $state(false);
	let draft = $state(false);
	let libraryOpen = $state(false);
	let selectedSequence = 0;
	let listSequence = 0;
	let offset = $derived(Math.max(0, Number($page.url.searchParams.get('offset')) || 0));
	let search = $derived($page.url.searchParams.get('q') || '');
	let kind = $derived($page.url.searchParams.get('entity_type') || '');
	let id = $derived($page.url.searchParams.get('entity_id') || '');
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
						: response.status === 404
							? m.wiki_unavailable()
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
		void request<WikiSettings>('/settings')
			.then((result) => {
				settings = result;
				if (selected?.status === 'draft') draft = result.can_write;
			})
			.catch((e) => {
				error = message(e);
			});
	});
	onDestroy(() => {
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
</script>

<svelte:head><title>{m.wiki_title()}</title></svelte:head>

<div class="min-h-full bg-[#f8f7f4] text-[#252522] dark:bg-gray-950 dark:text-gray-100">
	<div class="mx-auto max-w-[1600px] px-4 sm:px-7 xl:px-10">
		<header
			class="flex flex-wrap items-center justify-between gap-4 border-b border-[#e9e5de] py-5 dark:border-gray-800"
		>
			<div class="flex items-baseline gap-3">
				<h1 class="font-serif text-2xl tracking-tight">{m.wiki_title()}</h1>
				<span class="text-xs text-gray-400"
					>{m.wiki_page_count({ total: String(index.total) })}</span
				>
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
				>
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

		<div class="grid min-h-[calc(100vh-9rem)] items-start lg:grid-cols-[252px_minmax(0,1fr)]">
			<aside
				class="border-b border-[#e9e5de] py-5 dark:border-gray-800 lg:sticky lg:top-16 lg:min-h-[calc(100vh-9rem)] lg:border-b-0 lg:border-r lg:pr-5"
				aria-label={m.wiki_library()}
			>
				<button
					class="flex w-full items-center justify-between text-left lg:pointer-events-none"
					onclick={() => (libraryOpen = !libraryOpen)}
					aria-expanded={libraryOpen || !selected}
				>
					<span class="text-sm font-semibold text-gray-600 dark:text-gray-300"
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
				</div>
			</aside>

			<main aria-busy={pageLoading} class="min-w-0 bg-white dark:bg-gray-900">
				{#if pageLoading}
					<div class="mx-auto max-w-[900px] animate-pulse px-8 py-12">
						<div class="mb-7 h-4 w-32 bg-gray-100 dark:bg-gray-800"></div>
						<div class="mb-5 h-12 w-2/3 bg-gray-100 dark:bg-gray-800"></div>
						<div class="h-4 w-full bg-gray-100 dark:bg-gray-800"></div>
					</div>
				{:else if selected}
					<WikiArticle
						page={selected}
						{draft}
						{settings}
						{busy}
						onDraftChange={(value) => (draft = value)}
						onCompile={() => mutate('compile')}
						onPublish={() => mutate('publish')}
					/>
				{:else}
					<div class="mx-auto max-w-[900px] px-8 py-24">
						<h2 class="font-serif text-3xl">{m.wiki_select()}</h2>
						<p class="mt-3 text-sm text-gray-500">{m.wiki_select_help()}</p>
					</div>
				{/if}
			</main>
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
	:global(.dark) .wiki-action {
		border-color: #374151;
		color: #e5e7eb;
		background: #1f2937;
	}
</style>
