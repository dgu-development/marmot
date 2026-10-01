<script lang="ts">
	import Icon from '@iconify/svelte';
	import { locale } from '$lib/i18n';
	import { m } from '$lib/paraglide/messages';
	import type { MetamodelSchema } from '$lib/metamodel/types';
	import { nativeMessage } from '$lib/metamodel/i18n';
	import { resolveMessage } from '$lib/metamodel/labels';
	import { assetReferences, rememberAsset, type AssetReferences } from '$lib/assets/links';
	import AssetLinks from './AssetLinks.svelte';

	let {
		assetId,
		schema,
		onload = undefined
	}: {
		assetId: string;
		schema: MetamodelSchema;
		/** Receives how many assets point at this one, once known. */
		onload?: (count: number) => void;
	} = $props();

	let groups = $state<AssetReferences[]>([]);

	$effect(() => {
		let cancelled = false;
		groups = [];
		assetReferences(assetId)
			.then((found) => {
				if (cancelled) return;
				for (const group of found) group.assets.forEach(rememberAsset);
				groups = found;
				onload?.(new Set(found.flatMap((g) => g.assets.map((a) => a.id))).size);
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
	<div data-asset-references={group.field}>
		<div class="flex items-center gap-2 mb-2">
			<Icon icon="material-symbols:link-rounded" class="w-4 h-4 text-gray-500 dark:text-gray-400" />
			<h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider">
				{heading(group.field)}
			</h3>
		</div>
		<AssetLinks ids={group.assets.map((a) => a.id)} />
	</div>
{/each}
