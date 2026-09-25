<script lang="ts">
	import Icon from '@iconify/svelte';
	import { m } from '$lib/paraglide/messages';

	const MAX_BYTES = 256 * 1024;

	let {
		id,
		value = '',
		placeholder = '',
		required = false,
		accept = '.yaml,.yml,.json,.txt',
		onchange
	}: {
		id: string;
		value?: string;
		placeholder?: string;
		required?: boolean;
		accept?: string;
		onchange: (value: string) => void;
	} = $props();

	let dragging = $state(false);
	let error = $state('');
	let loaded = $state('');

	let lines = $derived(value ? value.split('\n').length : 0);

	async function load(files: FileList | null | undefined) {
		const file = files?.[0];
		if (!file) return;
		error = '';
		if (file.size > MAX_BYTES) {
			error = m.pipelines_text_too_large({ size: '256 KB' });
			return;
		}
		try {
			onchange(await file.text());
			loaded = file.name;
		} catch {
			error = m.pipelines_text_read_error();
		}
	}
</script>

<div
	class="overflow-hidden rounded-lg border transition-colors {dragging
		? 'border-earthy-terracotta-600 ring-2 ring-earthy-terracotta-600/30'
		: 'border-gray-300 dark:border-gray-600'}"
	role="group"
	ondragover={(e) => {
		e.preventDefault();
		dragging = true;
	}}
	ondragleave={() => (dragging = false)}
	ondrop={(e) => {
		e.preventDefault();
		dragging = false;
		load(e.dataTransfer?.files);
	}}
>
	<div
		class="flex items-center gap-2 border-b border-gray-200 bg-gray-50 px-2 py-1.5 dark:border-gray-600 dark:bg-gray-800"
	>
		<label
			class="inline-flex cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium text-gray-700 hover:bg-gray-200 dark:text-gray-200 dark:hover:bg-gray-700"
		>
			<Icon icon="material-symbols:upload-file-outline" class="h-4 w-4" />
			{m.pipelines_text_upload()}
			<input
				type="file"
				{accept}
				class="sr-only"
				onchange={(e) => {
					const input = e.currentTarget as HTMLInputElement;
					load(input.files);
					// Cleared so picking the same file again, after editing it, fires change.
					input.value = '';
				}}
			/>
		</label>
		<span class="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-gray-400">
			{loaded || m.pipelines_text_drop()}
		</span>
		{#if value}
			<span class="text-xs text-gray-400">{m.pipelines_text_lines({ count: lines })}</span>
			<button
				type="button"
				class="rounded-md p-1 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-gray-700 dark:hover:text-white"
				title={m.pipelines_text_clear()}
				aria-label={m.pipelines_text_clear()}
				onclick={() => {
					loaded = '';
					onchange('');
				}}
			>
				<Icon icon="material-symbols:close-rounded" class="h-4 w-4" />
			</button>
		{/if}
	</div>
	<textarea
		{id}
		{value}
		{placeholder}
		{required}
		rows="10"
		spellcheck="false"
		oninput={(e) => {
			loaded = '';
			onchange((e.currentTarget as HTMLTextAreaElement).value);
		}}
		class="block w-full resize-y border-0 bg-white px-3 py-2.5 font-mono text-xs leading-5 text-gray-900 focus:outline-none focus:ring-0 dark:bg-gray-700 dark:text-gray-100"
	></textarea>
</div>
{#if error}
	<p class="mt-1 text-xs text-red-600 dark:text-red-400" role="alert">{error}</p>
{/if}
