<script lang="ts">
	import Icon from '@iconify/svelte';
	import { locale } from '$lib/i18n';
	import { m } from '$lib/paraglide/messages';
	import type { MetamodelSchema } from '$lib/metamodel/types';
	import { nativeMessage } from '$lib/metamodel/i18n';
	import { resolveMessage } from '$lib/metamodel/labels';
	import { rememberTerm, termReferences, type TermReferences } from '$lib/glossary/links';
	import TermLinks from './TermLinks.svelte';

	let {
		termId,
		schema,
		onload = undefined
	}: {
		termId: string;
		schema: MetamodelSchema;
		/** Receives how many terms point at this one, once known. */
		onload?: (count: number) => void;
	} = $props();

	let groups = $state<TermReferences[]>([]);

	$effect(() => {
		let cancelled = false;
		groups = [];
		termReferences(termId)
			.then((found) => {
				if (cancelled) return;
				for (const group of found) group.terms.forEach(rememberTerm);
				groups = found;
				onload?.(new Set(found.flatMap((g) => g.terms.map((t) => t.id))).size);
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	});

	const context = $derived({
		locale: $locale,
		defaultLocale: schema.defaultLocale,
		messages: schema.messages,
		native: nativeMessage
	});

	function heading(fieldId: string): string {
		const field = schema.fields.find((f) => f.id === fieldId);
		const inverse = resolveMessage(field?.presentation?.inverseLabelKey, context);
		if (inverse) return inverse;
		const label = resolveMessage(field?.presentation?.labelKey, context) ?? fieldId;
		return m.glossary_referenced_by({ field: label });
	}
</script>

{#each groups as group (group.field)}
	<div data-term-references={group.field}>
		<div class="flex items-center gap-2 mb-2">
			<Icon icon="material-symbols:link-rounded" class="w-4 h-4 text-gray-500 dark:text-gray-400" />
			<h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider">
				{heading(group.field)}
			</h3>
		</div>
		<TermLinks ids={group.terms.map((t) => t.id)} />
	</div>
{/each}
