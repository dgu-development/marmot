<script lang="ts">
	import { locale } from '$lib/i18n';
	import type { MetamodelSchema } from '$lib/metamodel/types';
	import { nativeMessage } from '$lib/metamodel/i18n';
	import { resolveMessage, valueLabel } from '$lib/metamodel/labels';
	import { readMetadataValue } from '$lib/metamodel/values';

	let {
		schema,
		metadata,
		size = 'sm'
	}: {
		schema: MetamodelSchema | null | undefined;
		metadata: Record<string, unknown> | undefined;
		size?: 'xs' | 'sm';
	} = $props();

	const context = $derived({
		locale: $locale,
		defaultLocale: schema?.defaultLocale ?? 'en',
		messages: schema?.messages,
		native: nativeMessage
	});

	type Part = { id: string; field: string; text: string };

	// A badge derived from another one (asset family from asset type) joins it in one chip,
	// read from the general to the specific.
	const chips = $derived.by(() => {
		if (!schema?.enabled) return [];
		const parts: Record<string, Part> = {};
		const order: string[] = [];
		for (const field of schema.fields) {
			if (!field.presentation?.badge) continue;
			const value = readMetadataValue(metadata, field.storage);
			if (typeof value !== 'string' || value === '') continue;
			order.push(field.id);
			parts[field.id] = {
				id: field.id,
				field: resolveMessage(field.presentation.labelKey, context) ?? field.id,
				text: valueLabel(field, value, context) ?? value
			};
		}
		const joined: Record<string, true> = {};
		const out: Part[][] = [];
		for (const field of schema.fields) {
			const source = field.derive?.from;
			if (!parts[field.id] || !source || !parts[source]) continue;
			out.push([parts[field.id], parts[source]]);
			joined[field.id] = true;
			joined[source] = true;
		}
		for (const id of order) if (!joined[id]) out.push([parts[id]]);
		return out;
	});
</script>

{#each chips as chip (chip.map((part) => part.id).join('+'))}
	<span
		class="inline-flex flex-shrink-0 items-center overflow-hidden rounded-full border border-earthy-terracotta-300 bg-earthy-terracotta-50 font-medium text-earthy-terracotta-800 dark:border-earthy-terracotta-800 dark:bg-earthy-terracotta-900/30 dark:text-earthy-terracotta-200 {size ===
		'sm'
			? 'text-sm'
			: 'text-xs'}"
		title={chip.map((part) => `${part.field}: ${part.text}`).join(' · ')}
	>
		{#each chip as part, index (part.id)}
			<span
				class="{size === 'sm' ? 'px-2.5 py-0.5' : 'px-2 py-0.5'} {chip.length > 1 && index === 0
					? 'bg-earthy-terracotta-600 text-white dark:bg-earthy-terracotta-700'
					: ''}"
				data-field-badge={part.id}>{part.text}</span
			>
		{/each}
	</span>
{/each}
