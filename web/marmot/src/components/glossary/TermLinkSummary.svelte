<script lang="ts">
	import { resolveTerms, type TermRef } from '$lib/glossary/links';

	let { label, ids }: { label: string; ids: string[] } = $props();

	let terms = $state<(TermRef | null)[]>([]);

	$effect(() => {
		let cancelled = false;
		resolveTerms(ids)
			.then((resolved) => {
				if (!cancelled) terms = ids.map((id) => resolved.get(id) ?? null);
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	});

	const names = $derived(terms.filter((t): t is TermRef => t !== null).map((t) => t.name));
</script>

{#if names.length > 0}
	<div class="mt-0.5 truncate text-xs text-earthy-terracotta-700 dark:text-earthy-terracotta-400">
		{label}: {names.join(', ')}
	</div>
{/if}
