<script lang="ts">
	import { onMount } from 'svelte';
	import Button from '$components/ui/Button.svelte';
	import { auth } from '$lib/stores/auth';
	import { toasts } from '$lib/stores/toast';
	import type { Asset } from '$lib/assets/types';
	import { m } from '$lib/paraglide/messages';
	import {
		fetchMetamodel,
		isValidationError,
		MetamodelHttpError,
		patchAssetFields
	} from '$lib/metamodel/api';
	import type { MetamodelField, MetamodelSchema, MetamodelViolation } from '$lib/metamodel/types';
	import { configurableFields, readMetadataValue } from '$lib/metamodel/values';

	let {
		asset = $bindable(),
		onSaved
	}: {
		asset: Asset;
		onSaved?: (next: Asset) => void;
	} = $props();

	let schema = $state<MetamodelSchema | null>(null);
	let loadError = $state('');
	let values = $state<Record<string, unknown>>({});
	let saving = $state(false);
	let violations = $state<MetamodelViolation[]>([]);

	const fields = $derived(schema ? configurableFields(schema.fields) : []);
	const canEdit = $derived(auth.hasPermission('assets', 'manage'));
	const showForm = $derived(!!schema?.enabled && fields.length > 0);

	onMount(() => {
		void loadSchema();
	});

	$effect(() => {
		if (!schema || !asset) return;
		const next: Record<string, unknown> = {};
		for (const field of configurableFields(schema.fields)) {
			next[field.id] = readMetadataValue(asset.metadata, field.storage) ?? '';
		}
		values = next;
		violations = [];
	});

	async function loadSchema() {
		try {
			schema = await fetchMetamodel();
			loadError = '';
		} catch (err) {
			loadError = err instanceof Error ? err.message : String(err);
			schema = null;
		}
	}

	function labelFor(field: MetamodelField): string {
		const key = field.presentation?.labelKey;
		if (key) {
			const candidates = [key, key.replace(/\./g, '_')];
			const catalog = m as Record<string, (() => string) | undefined>;
			for (const candidate of candidates) {
				const fn = catalog[candidate];
				if (typeof fn === 'function') return fn();
			}
		}
		return field.id;
	}

	function coerce(field: MetamodelField, raw: unknown): unknown {
		if (raw === '' || raw === undefined) return field.nullable ? null : undefined;
		switch (field.type) {
			case 'integer': {
				const n = typeof raw === 'number' ? raw : Number(raw);
				return Number.isInteger(n) ? n : raw;
			}
			case 'number': {
				const n = typeof raw === 'number' ? raw : Number(raw);
				return Number.isFinite(n) ? n : raw;
			}
			case 'boolean':
				return raw === true || raw === 'true';
			default:
				return raw;
		}
	}

	function violationFor(id: string): string | undefined {
		return violations.find((v) => v.field === id)?.code;
	}

	async function save() {
		if (!canEdit || !asset?.id) return;
		const version = asset.version;
		if (version == null || version < 1) {
			toasts.error(m.metamodel_missing_version());
			return;
		}
		const payload: Record<string, unknown> = {};
		for (const field of fields) {
			const coerced = coerce(field, values[field.id]);
			if (coerced === undefined) continue;
			payload[field.id] = coerced;
		}
		saving = true;
		violations = [];
		try {
			const updated = await patchAssetFields(asset.id, version, payload);
			asset = { ...asset, ...updated, version: updated.version ?? version + 1 };
			onSaved?.(asset);
			toasts.success(m.common_save());
		} catch (err) {
			if (err instanceof MetamodelHttpError) {
				if (err.status === 412) {
					toasts.error(m.metamodel_conflict());
				} else if (err.status === 400 && isValidationError(err.body)) {
					violations = err.body.fields;
					toasts.error(m.metamodel_validation_failed());
				} else {
					toasts.error(m.metamodel_save_failed());
				}
			} else {
				toasts.error(m.metamodel_save_failed());
			}
		} finally {
			saving = false;
		}
	}
</script>

{#if loadError}
	<p class="mt-4 text-sm text-red-600 dark:text-red-400">{loadError}</p>
{:else if showForm}
	<section class="mt-6 mb-8">
		<div class="flex items-center justify-between gap-4 mb-4">
			<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100">
				{m.metamodel_governed_heading()}
			</h3>
			{#if canEdit}
				<Button text={m.common_save()} loading={saving} disabled={saving} click={save} />
			{/if}
		</div>
		<div class="space-y-4">
			{#each fields as field (field.id)}
				<label class="block">
					<span class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
						{labelFor(field)}
						{#if field.required}<span class="text-red-500">*</span>{/if}
					</span>
					{#if field.type === 'boolean'}
						<input
							type="checkbox"
							class="rounded border-gray-300 dark:border-gray-600"
							checked={values[field.id] === true || values[field.id] === 'true'}
							disabled={!canEdit || saving}
							onchange={(e) => {
								values = { ...values, [field.id]: e.currentTarget.checked };
							}}
						/>
					{:else if field.type === 'enum' && field.values}
						<select
							class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-3 py-2 text-sm"
							disabled={!canEdit || saving}
							value={String(values[field.id] ?? '')}
							onchange={(e) => {
								values = { ...values, [field.id]: e.currentTarget.value };
							}}
						>
							<option value="">—</option>
							{#each field.values as option (option)}
								<option value={option}>{option}</option>
							{/each}
						</select>
					{:else if field.type === 'integer' || field.type === 'number'}
						<input
							type="number"
							step={field.type === 'integer' ? '1' : 'any'}
							class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-3 py-2 text-sm"
							disabled={!canEdit || saving}
							value={values[field.id] ?? ''}
							oninput={(e) => {
								values = { ...values, [field.id]: e.currentTarget.value };
							}}
						/>
					{:else if field.type === 'date'}
						<input
							type="date"
							class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-3 py-2 text-sm"
							disabled={!canEdit || saving}
							value={String(values[field.id] ?? '')}
							oninput={(e) => {
								values = { ...values, [field.id]: e.currentTarget.value };
							}}
						/>
					{:else}
						<input
							type="text"
							class="w-full rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-3 py-2 text-sm"
							disabled={!canEdit || saving}
							value={String(values[field.id] ?? '')}
							oninput={(e) => {
								values = { ...values, [field.id]: e.currentTarget.value };
							}}
						/>
					{/if}
					{#if violationFor(field.id)}
						<span class="mt-1 block text-xs text-red-600 dark:text-red-400">
							{violationFor(field.id)}
						</span>
					{/if}
				</label>
			{/each}
		</div>
	</section>
{/if}
