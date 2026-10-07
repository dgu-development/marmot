<script lang="ts">
	import MarkdownRenderer from '$components/ui/MarkdownRenderer.svelte';
	import { m } from '$lib/paraglide/messages';

	let { query, language = '' }: { query: string; language?: string } = $props();

	let copied = $state(false);

	const markdown = $derived(fencedQuery(query, language));
	const caption = $derived(language.trim() || 'SQL');

	function fenceLanguage(name: string): string {
		const normalized = name.trim().toLowerCase();
		if (!normalized || normalized === 'sql' || normalized === 'hiveql' || normalized === 'cql')
			return 'sql';
		if (['json', 'python', 'javascript', 'typescript', 'bash', 'yaml'].includes(normalized))
			return normalized;
		return 'text';
	}

	// A query that itself contains fences needs a longer opener, or marked closes it early.
	function fencedQuery(source: string, name: string): string {
		const body = source.endsWith('\n') ? source.slice(0, -1) : source;
		let run = 0;
		let longest = 2;
		for (const char of body) {
			if (char === '`') {
				run += 1;
				if (run > longest) longest = run;
			} else {
				run = 0;
			}
		}
		const fence = '`'.repeat(longest + 1);
		return `${fence}${fenceLanguage(name)}\n${body}\n${fence}`;
	}

	async function copyQuery() {
		try {
			await navigator.clipboard.writeText(query);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			copied = false;
		}
	}
</script>

<article
	class="query-view overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900"
>
	<header
		class="flex items-center justify-between gap-3 border-b border-gray-200 bg-gray-50 px-4 py-2 dark:border-gray-700 dark:bg-gray-800"
	>
		<span
			class="font-mono text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400"
			>{caption}</span
		>
		<button
			type="button"
			class="inline-flex items-center rounded px-2 py-1 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 {copied
				? 'bg-green-100 text-green-800 dark:bg-green-900/20 dark:text-green-100'
				: 'text-gray-600 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700'}"
			onclick={copyQuery}
		>
			{copied ? m.common_copied() : m.common_copy()}
		</button>
	</header>
	<MarkdownRenderer content={markdown} />
</article>

<style>
	/* Same earthy code colors as the documentation viewer. Prism's one-dark theme would paint this fence purple. */
	.query-view :global(.prose pre) {
		margin: 0 !important;
		border-radius: 0 !important;
	}
	.query-view :global(.token.comment) {
		color: #4a674a !important;
		font-style: italic;
	}
	.query-view :global(.token.keyword) {
		color: #8d3718 !important;
	}
	.query-view :global(.token.string),
	.query-view :global(.token.number),
	.query-view :global(.token.boolean) {
		color: #35593b !important;
	}
	.query-view :global(.token.function) {
		color: #b34822 !important;
	}
	.query-view :global(.token.operator),
	.query-view :global(.token.punctuation) {
		color: #4a674a !important;
	}
	:global(.dark) .query-view :global(.token.comment) {
		color: #a8c5a8 !important;
	}
	:global(.dark) .query-view :global(.token.keyword) {
		color: #ffa77d !important;
	}
	:global(.dark) .query-view :global(.token.string),
	:global(.dark) .query-view :global(.token.number),
	:global(.dark) .query-view :global(.token.boolean) {
		color: #b9d9b9 !important;
	}
	:global(.dark) .query-view :global(.token.function) {
		color: #ffb899 !important;
	}
	:global(.dark) .query-view :global(.token.operator),
	:global(.dark) .query-view :global(.token.punctuation) {
		color: #d1e5d1 !important;
	}
</style>
