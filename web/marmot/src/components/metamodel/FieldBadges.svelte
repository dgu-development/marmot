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

	const badges = $derived.by(() => {
		if (!schema?.enabled) return [];
		const out: { id: string; field: string; text: string }[] = [];
		for (const field of schema.fields) {
			if (!field.presentation?.badge) continue;
			const value = readMetadataValue(metadata, field.storage);
			if (typeof value !== 'string' || value === '') continue;
			out.push({
				id: field.id,
				field: resolveMessage(field.presentation.labelKey, context) ?? field.id,
				text: valueLabel(field, value, context) ?? value
			});
		}
		return out;
	});
</script>

{#each badges as badge (badge.id)}
	<span
		class="inline-flex flex-shrink-0 items-center rounded-full border border-earthy-terracotta-300 bg-earthy-terracotta-50 font-medium text-earthy-terracotta-800 dark:border-earthy-terracotta-800 dark:bg-earthy-terracotta-900/30 dark:text-earthy-terracotta-200 {size ===
		'sm'
			? 'px-2.5 py-0.5 text-sm'
			: 'px-2 py-0.5 text-xs'}"
		title={badge.field}
		data-field-badge={badge.id}>{badge.text}</span
	>
{/each}
