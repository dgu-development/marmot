<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve -- entity and evidence URLs are supplied by the catalog API. */
	import Icon from '@iconify/svelte';
	import MarkdownRenderer from '$components/ui/MarkdownRenderer.svelte';
	import MemoryPanel from '$components/memory/MemoryPanel.svelte';
	import { fetchApi } from '$lib/api';
	import { auth } from '$lib/stores/auth';
	import { m } from '$lib/paraglide/messages';
	import type { Page, PageTree } from '$lib/docs/types';
	import type { WikiPage, WikiSettings } from '$lib/knowledge/types';

	let {
		page,
		draft,
		settings,
		busy,
		onDraftChange,
		onCompile,
		onPublish
	}: {
		page: WikiPage;
		draft: boolean;
		settings: WikiSettings;
		busy: boolean;
		onDraftChange: (value: boolean) => void;
		onCompile: () => void;
		onPublish: () => void;
	} = $props();

	type Section = Page & { depth: number };
	let docs = $state<Page[]>([]);
	let docsLoading = $state(false);
	let docsError = $state('');
	let editingId = $state<string | null>(null);
	let editTitle = $state('');
	let editContent = $state('');
	let saving = $state(false);
	let docsSeq = 0;

	let canEditDocs = $derived(auth.hasPermission('assets', 'manage'));
	let supportsDocs = $derived(page.entity_type === 'asset' || page.entity_type === 'data_product');
	let sources = $derived((draft ? page.draft_sources : page.published_sources) || []);
	let facts = $derived(
		sources
			.filter(
				(source) =>
					source.id.startsWith(`${page.entity_type}:${page.entity_id}:`) &&
					![
						'name',
						'description',
						'user_description',
						'definition',
						'user_definition',
						'metadata',
						'schema'
					].includes(source.field)
			)
			.slice(0, 8)
	);
	let relations = $derived(
		sources.filter((source) => source.id.startsWith('relation:')).slice(0, 12)
	);
	let imported = $derived(
		sources.filter((source) => /^(imported-doc:|global-doc:)/.test(source.id))
	);
	let sections = $derived(flatten(docs));

	function flatten(pages: Page[], depth = 0): Section[] {
		return pages.flatMap((item) => [
			{ ...item, depth },
			...flatten(item.children || [], depth + 1)
		]);
	}

	async function loadDocs(entityType = page.entity_type, docEntityID = page.doc_entity_id) {
		const seq = ++docsSeq;
		if ((entityType !== 'asset' && entityType !== 'data_product') || !docEntityID) {
			docs = [];
			return;
		}
		docsLoading = true;
		docsError = '';
		try {
			const response = await fetchApi(
				`/docs/entity/${entityType}/${encodeURIComponent(docEntityID)}/pages`
			);
			if (!response.ok) throw new Error(m.wiki_error());
			const tree: PageTree = await response.json();
			if (seq === docsSeq) docs = tree.pages;
		} catch (error) {
			if (seq === docsSeq) docsError = error instanceof Error ? error.message : m.wiki_error();
		} finally {
			if (seq === docsSeq) docsLoading = false;
		}
	}

	$effect(() => {
		void loadDocs(page.entity_type, page.doc_entity_id);
	});

	function edit(section?: Page) {
		editingId = section?.id || 'new';
		editTitle = section?.title || '';
		editContent = section?.content || '';
	}

	async function saveSection() {
		const title = editTitle.trim();
		if (!title || !supportsDocs || !page.doc_entity_id) return;
		saving = true;
		docsError = '';
		try {
			const path =
				editingId === 'new'
					? `/docs/entity/${page.entity_type}/${encodeURIComponent(page.doc_entity_id)}/pages`
					: `/docs/pages/${editingId}`;
			const response = await fetchApi(path, {
				method: editingId === 'new' ? 'POST' : 'PUT',
				body: JSON.stringify({ title, content: editContent })
			});
			if (!response.ok) throw new Error(m.wiki_error());
			editingId = null;
			await loadDocs();
		} catch (error) {
			docsError = error instanceof Error ? error.message : m.wiki_error();
		} finally {
			saving = false;
		}
	}

	function kindLabel(kind: string) {
		return (
			(
				{
					asset: m.wiki_asset,
					data_product: m.wiki_data_product,
					glossary_term: m.wiki_glossary_term,
					domain: m.wiki_domain
				} as Record<string, () => string>
			)[kind]?.() || kind
		);
	}

	function readable(field: string) {
		return field.replaceAll('_', ' ');
	}
</script>

<div class="min-w-0 bg-white dark:bg-gray-900 xl:grid xl:grid-cols-[minmax(0,1fr)_292px]">
	<article class="mx-auto w-full max-w-[900px] px-6 pb-24 pt-9 sm:px-10 lg:px-14">
		<div class="mb-7 flex flex-wrap items-center gap-2 text-sm text-gray-500">
			<span>{m.wiki_title()}</span><Icon
				icon="material-symbols:chevron-right-rounded"
				class="h-4 w-4"
			/>
			<span>{kindLabel(page.entity_type)}</span>
		</div>
		<header class="border-b border-gray-200 pb-7 dark:border-gray-700">
			<h1
				class="wiki-heading break-words text-[2.5rem] leading-[1.12] tracking-tight text-gray-950 dark:text-white sm:text-[3.5rem]"
			>
				{page.title}
			</h1>
			<div class="mt-5 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
				<a
					href={page.entity_url}
					class="inline-flex items-center gap-1.5 font-medium text-earthy-terracotta-700 hover:underline dark:text-earthy-terracotta-400"
				>
					{m.wiki_entity()}<Icon icon="material-symbols:arrow-outward-rounded" class="h-4 w-4" />
				</a>
				<span class="text-gray-400"
					>{page.freshness === 'fresh' ? m.wiki_fresh() : m.wiki_stale()}</span
				>
			</div>
		</header>

		{#if settings.can_write && page.draft_hash && page.draft_freshness === 'fresh' && page.draft_mode === 'llm'}
			<div
				class="mt-6 flex flex-wrap items-center gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm dark:border-amber-900 dark:bg-amber-950"
			>
				<span class="min-w-0 flex-1 text-amber-900 dark:text-amber-200">{m.wiki_review_help()}</span
				>
				<button
					class="font-medium text-earthy-terracotta-700 hover:underline"
					onclick={() => onDraftChange(!draft)}
					>{draft ? m.wiki_published_view() : m.wiki_draft_view()}</button
				>
				{#if draft}<button
						class="rounded-md bg-earthy-terracotta-700 px-3 py-2 font-medium text-white disabled:opacity-50"
						disabled={busy}
						onclick={onPublish}>{m.wiki_publish()}</button
					>{/if}
			</div>
		{/if}

		{#if page.freshness === 'stale' && !draft}
			<p
				class="mt-6 rounded-md bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200"
			>
				{m.wiki_stale_help()}
			</p>
		{/if}

		<section id="wiki-summary" class="scroll-mt-24 pt-9">
			<h2 class="wiki-section-heading">{m.wiki_description()}</h2>
			{#if page.description}
				<p
					class="mt-4 whitespace-pre-line break-words text-[1.06rem] leading-8 text-gray-700 dark:text-gray-200"
				>
					{page.description}
				</p>
			{:else}
				<p class="mt-4 text-sm leading-7 text-gray-500">{m.wiki_no_description()}</p>
			{/if}
		</section>

		{#if draft && page.draft_mode === 'llm' && page.draft_content}
			<section class="wiki-prose mt-10 border-t border-gray-200 pt-8 dark:border-gray-700">
				<MarkdownRenderer content={page.draft_content} />
			</section>
		{:else if page.mode === 'llm' && page.content && page.freshness === 'fresh'}
			<section class="wiki-prose mt-10 border-t border-gray-200 pt-8 dark:border-gray-700">
				<MarkdownRenderer content={page.content} />
			</section>
		{/if}

		{#if supportsDocs}
			<section
				id="wiki-sections"
				class="mt-12 scroll-mt-24 border-t border-gray-200 pt-8 dark:border-gray-700"
			>
				<div class="flex items-center justify-between gap-4">
					<h2 class="wiki-section-heading">{m.wiki_sections()}</h2>
					{#if canEditDocs && editingId !== 'new'}<button class="wiki-link" onclick={() => edit()}
							><Icon
								icon="material-symbols:add-rounded"
								class="h-4 w-4"
							/>{m.wiki_add_section()}</button
						>{/if}
				</div>
				{#if docsError}<p role="alert" class="mt-4 text-sm text-red-700">{docsError}</p>{/if}
				{#if docsLoading}<p class="mt-5 text-sm text-gray-500">{m.wiki_loading()}</p>{/if}
				{#if sections.length === 0 && !docsLoading && editingId !== 'new'}<p
						class="mt-5 text-sm leading-7 text-gray-500"
					>
						{m.wiki_no_sections()}
					</p>{/if}
				{#each sections as section (section.id)}
					<div class="mt-9" id={`wiki-section-${section.id}`}>
						{#if editingId === section.id}
							<div class="wiki-editor">
								<label class="block text-xs font-medium text-gray-600" for="wiki-section-title"
									>{m.wiki_section_title()}</label
								>
								<input
									id="wiki-section-title"
									class="wiki-input"
									maxlength="255"
									bind:value={editTitle}
								/>
								<label
									class="mt-4 block text-xs font-medium text-gray-600"
									for="wiki-section-content">{m.wiki_section_content()}</label
								>
								<textarea
									id="wiki-section-content"
									class="wiki-input min-h-56 font-mono text-sm"
									bind:value={editContent}
								></textarea>
								<div class="mt-4 flex justify-end gap-3">
									<button class="wiki-link" onclick={() => (editingId = null)}
										>{m.common_cancel()}</button
									><button
										class="wiki-save"
										disabled={saving || !editTitle.trim()}
										onclick={saveSection}>{m.common_save()}</button
									>
								</div>
							</div>
						{:else}
							<div
								class="flex items-baseline justify-between gap-3 border-b border-gray-200 pb-2 dark:border-gray-700"
							>
								<h3
									class="wiki-subheading"
									style={`padding-left: ${Math.min(section.depth, 3) * 0.75}rem`}
								>
									{section.title}
								</h3>
								{#if canEditDocs}<button class="wiki-link text-xs" onclick={() => edit(section)}
										>{m.wiki_edit()}</button
									>{/if}
							</div>
							{#if section.content}<div class="wiki-prose mt-4">
									<MarkdownRenderer content={section.content} />
								</div>{:else}<p class="mt-4 text-sm text-gray-400">{m.wiki_empty_section()}</p>{/if}
						{/if}
					</div>
				{/each}
				{#if editingId === 'new'}
					<div class="wiki-editor mt-8">
						<label class="block text-xs font-medium text-gray-600" for="wiki-new-title"
							>{m.wiki_section_title()}</label
						>
						<input id="wiki-new-title" class="wiki-input" maxlength="255" bind:value={editTitle} />
						<label class="mt-4 block text-xs font-medium text-gray-600" for="wiki-new-content"
							>{m.wiki_section_content()}</label
						>
						<textarea
							id="wiki-new-content"
							class="wiki-input min-h-56 font-mono text-sm"
							bind:value={editContent}
						></textarea>
						<div class="mt-4 flex justify-end gap-3">
							<button class="wiki-link" onclick={() => (editingId = null)}
								>{m.common_cancel()}</button
							><button
								class="wiki-save"
								disabled={saving || !editTitle.trim()}
								onclick={saveSection}>{m.common_save()}</button
							>
						</div>
					</div>
				{/if}
			</section>
		{/if}

		{#if page.entity_type === 'asset' || page.entity_type === 'data_product'}
			<section
				id="wiki-memory"
				class="mt-12 scroll-mt-24 border-t border-gray-200 pt-8 dark:border-gray-700"
			>
				<h2 class="wiki-section-heading">{m.wiki_memory()}</h2>
				<p class="mt-2 text-sm leading-6 text-gray-500">{m.wiki_memory_help()}</p>
				<div class="mt-6">
					{#key `${page.entity_type}:${page.entity_id}:${page.current_source_hash}`}<MemoryPanel
							entityType={page.entity_type}
							entityId={page.entity_id}
							showHeading={false}
						/>{/key}
				</div>
			</section>
		{/if}

		{#if imported.length}
			<section class="mt-12 border-t border-gray-200 pt-8 dark:border-gray-700">
				<h2 class="wiki-section-heading">{m.wiki_group_docs()}</h2>
				<div class="mt-5 space-y-5">
					{#each imported as source (source.id)}<div>
							<h3 class="font-medium">{source.title}</h3>
							<p class="mt-1 break-words text-sm leading-6 text-gray-600 dark:text-gray-300">
								{source.preview}
							</p>
						</div>{/each}
				</div>
			</section>
		{/if}

		<details class="mt-12 border-t border-gray-200 pt-6 text-sm dark:border-gray-700">
			<summary
				class="cursor-pointer font-medium text-gray-600 hover:text-earthy-terracotta-700 dark:text-gray-300"
				>{m.wiki_sources()}
				<span class="font-normal text-gray-400">({sources.length})</span></summary
			>
			<ul class="mt-5 space-y-2">
				{#each sources as source (source.id)}<li>
						<a
							class="break-words text-earthy-terracotta-700 hover:underline dark:text-earthy-terracotta-400"
							href={source.url}>{source.title}</a
						><span class="ml-2 text-xs text-gray-400">{source.field}</span>
					</li>{/each}
			</ul>
		</details>
	</article>

	<aside
		class="border-t border-gray-200 bg-[#f8f8f7] px-6 py-8 dark:border-gray-700 dark:bg-gray-950 xl:sticky xl:top-16 xl:min-h-[calc(100vh-9rem)] xl:self-start xl:border-l xl:border-t-0 xl:px-6"
		aria-label={m.wiki_facts()}
	>
		<div class="border-b border-gray-200 pb-5 dark:border-gray-700">
			<p class="text-xs font-semibold text-gray-500">{kindLabel(page.entity_type)}</p>
			<h2 class="mt-2 break-words text-lg font-semibold leading-6">{page.title}</h2>
		</div>
		<section class="pt-6">
			<h3 class="text-sm font-semibold">{m.wiki_facts()}</h3>
			{#if facts.length}<dl class="mt-3 divide-y divide-gray-200 dark:divide-gray-800">
					{#each facts as fact (fact.id)}<div class="py-2.5">
							<dt class="text-xs text-gray-500">{readable(fact.field)}</dt>
							<dd class="mt-0.5 break-words text-sm leading-5">{fact.preview}</dd>
						</div>{/each}
				</dl>{:else}<p class="mt-3 text-sm leading-6 text-gray-500">{m.wiki_no_facts()}</p>{/if}
		</section>
		<section class="mt-7 border-t border-gray-200 pt-6 dark:border-gray-700">
			<h3 class="text-sm font-semibold">{m.wiki_group_relations()}</h3>
			{#if relations.length}<ul class="mt-3 space-y-3">
					{#each relations as relation (relation.id)}<li>
							<span class="block text-xs text-gray-500">{readable(relation.field)}</span><a
								class="mt-0.5 block break-words text-sm font-medium text-earthy-terracotta-700 hover:underline dark:text-earthy-terracotta-400"
								href={relation.url}>{relation.title}</a
							>
						</li>{/each}
				</ul>{:else}<p class="mt-3 text-sm leading-6 text-gray-500">{m.wiki_no_relations()}</p>{/if}
		</section>
		{#if settings.can_write}<details
				class="mt-8 border-t border-gray-200 pt-5 text-xs dark:border-gray-700"
			>
				<summary class="cursor-pointer text-gray-500">{m.wiki_maintenance()}</summary><button
					class="wiki-link mt-3"
					disabled={busy}
					onclick={onCompile}>{m.wiki_compile()}</button
				>
			</details>{/if}
	</aside>
</div>

<style>
	.wiki-heading {
		font-family: Georgia, 'Times New Roman', serif;
		font-weight: 400;
	}
	.wiki-section-heading {
		font-family: Georgia, 'Times New Roman', serif;
		font-size: 1.65rem;
		line-height: 1.25;
		color: inherit;
	}
	.wiki-subheading {
		font-family: Georgia, 'Times New Roman', serif;
		font-size: 1.3rem;
		line-height: 1.3;
	}
	.wiki-link {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		color: #a94728;
		font-weight: 600;
	}
	.wiki-link:hover {
		text-decoration: underline;
	}
	.wiki-save {
		border-radius: 0.375rem;
		background: #a94728;
		padding: 0.5rem 0.9rem;
		color: white;
		font-weight: 600;
	}
	.wiki-save:disabled {
		opacity: 0.5;
	}
	.wiki-editor {
		border: 1px solid #e5e7eb;
		border-radius: 0.5rem;
		padding: 1rem;
		background: #fafaf9;
	}
	.wiki-input {
		display: block;
		width: 100%;
		margin-top: 0.4rem;
		border: 1px solid #d1d5db;
		border-radius: 0.375rem;
		padding: 0.65rem 0.75rem;
		background: white;
		color: #111827;
	}
	.wiki-input:focus {
		outline: 2px solid #c2775b;
		outline-offset: 1px;
	}
	.wiki-prose :global(.prose) {
		max-width: none;
		font-size: 1rem;
		line-height: 1.85;
	}
	:global(.dark) .wiki-editor {
		border-color: #374151;
		background: #111827;
	}
	:global(.dark) .wiki-input {
		border-color: #4b5563;
		background: #1f2937;
		color: white;
	}
</style>
