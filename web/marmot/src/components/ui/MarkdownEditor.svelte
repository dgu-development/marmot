<script lang="ts">
	import MarkdownRenderer from './MarkdownRenderer.svelte';
	import { locale } from '$lib/i18n';
	import { m } from '$lib/paraglide/messages';

	export let value: string;
	export let id: string | undefined = undefined;
	export let ariaLabel: string | undefined = undefined;
	export let placeholder: string = '';
	export let rows: number = 4;
	export let disabled: boolean = false;
	export let showPreview: boolean = true;

	let previewMode = false;
</script>

<div class="space-y-2">
	{#if showPreview}
		<div class="flex gap-2 border-b border-gray-200 dark:border-gray-700">
			<button
				type="button"
				on:click={() => (previewMode = false)}
				aria-pressed={!previewMode}
				class="px-3 py-1.5 text-sm font-medium {previewMode
					? 'text-gray-500 dark:text-gray-400'
					: 'text-earthy-terracotta-700 dark:text-earthy-terracotta-700 border-b-2 border-earthy-terracotta-700 dark:border-earthy-terracotta-500'}"
			>
				{m.ui_markdown_write_tab()}
			</button>
			<button
				type="button"
				on:click={() => (previewMode = true)}
				aria-pressed={previewMode}
				class="px-3 py-1.5 text-sm font-medium {previewMode
					? 'text-earthy-terracotta-700 dark:text-earthy-terracotta-700 border-b-2 border-earthy-terracotta-700 dark:border-earthy-terracotta-500'
					: 'text-gray-500 dark:text-gray-400'}"
			>
				{m.ui_markdown_preview_tab()}
			</button>
		</div>
	{/if}

	{#if previewMode && showPreview}
		<div
			class="min-h-[100px] p-3 border border-gray-300 dark:border-gray-600 rounded-md bg-gray-50 dark:bg-gray-900"
		>
			{#if value}
				<MarkdownRenderer content={value} />
			{:else}
				<p class="text-sm text-gray-400 dark:text-gray-500 italic">
					{m.ui_markdown_nothing_to_preview()}
				</p>
			{/if}
		</div>
	{:else}
		<textarea
			{id}
			aria-label={ariaLabel}
			bind:value
			{placeholder}
			{rows}
			{disabled}
			class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md shadow-sm focus:ring-earthy-terracotta-600 focus:border-earthy-terracotta-700 dark:bg-gray-700 dark:text-gray-100 disabled:opacity-50 font-mono text-sm"
		></textarea>
	{/if}
	{#if showPreview}
		<p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
			{m.ui_markdown_hint()}
			<a
				class="underline underline-offset-2 hover:text-earthy-terracotta-700"
				href={`https://docs.github.com/${$locale === 'es' ? 'es' : 'en'}/get-started/writing-on-github/getting-started-with-writing-and-formatting-on-github/basic-writing-and-formatting-syntax`}
				target="_blank"
				rel="noopener noreferrer">{m.ui_markdown_guide()}</a
			>
		</p>
	{/if}
</div>
