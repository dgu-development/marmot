<script lang="ts">
	import { tick } from 'svelte';
	import SearchLinks from '$components/metamodel/SearchLinks.svelte';
	import { resolve } from '$app/paths';
	import IconifyIcon from '@iconify/svelte';
	import { toasts } from '$lib/stores/toast';
	import { locale } from '$lib/i18n';
	import { m } from '$lib/paraglide/messages';
	import Avatar from '$components/user/Avatar.svelte';
	import AssetLinks from '$components/asset/AssetLinks.svelte';
	import AssetLinkPicker from '$components/asset/AssetLinkPicker.svelte';
	import {
		ASSET_CONTROL,
		assetLinkIds,
		assetReferences,
		rememberAsset,
		type AssetReferences
	} from '$lib/assets/links';
	import { createKeyboardNavigationState } from '$lib/keyboard';
	import type { Asset } from '$lib/assets/types';
	import type { MetamodelField, MetamodelSchema } from '$lib/metamodel/types';
	import { isValidationError, MetamodelHttpError, patchAssetFields } from '$lib/metamodel/api';
	import { nativeMessage, violationMessage } from '$lib/metamodel/i18n';
	import { resolveMessage, valueLabel } from '$lib/metamodel/labels';
	import {
		lookupOwnerById,
		searchUsers as searchUserOwners,
		type OwnerResult
	} from '$lib/metamodel/owners';
	import {
		draftFromValue,
		facetHref,
		isHttpUrl,
		isUnset,
		readMetadataValue,
		sameValue,
		sectionLabel,
		toPayload,
		typeIcon,
		typeLabel,
		valueClass,
		type Draft,
		orderedValues
	} from '$lib/metamodel/values';

	let {
		asset = $bindable(),
		schema,
		fields,
		editable = false,
		hiddenIncoming = [],
		onConflict
	}: {
		asset: Asset;
		schema: MetamodelSchema;
		fields: MetamodelField[];
		editable?: boolean;
		/** Fields whose incoming links are shown elsewhere on the page. */
		hiddenIncoming?: string[];
		onConflict?: () => void;
	} = $props();

	let editingId = $state<string | null>(null);
	let draft = $state<Draft>('');
	let pending = $state('');
	let errorCode = $state<string | null>(null);
	let saving = $state(false);

	// The enum/boolean editor: a single listbox can be open at a time, matching LanguageSelector.
	let listboxOpen = $state(false);
	let listboxRoot = $state<HTMLDivElement>();

	// A single search box serves whichever user-control field is being edited; only one is ever open.
	let userQuery = $state('');
	let userResults = $state<OwnerResult[]>([]);
	let userSearching = $state(false);
	let userFocusedIndex = $state(-1);
	let userSearchTimeout: ReturnType<typeof setTimeout>;

	// Resolves a stored steward id to a name once, shared by the display and the editor's chip.
	let resolvedOwners = $state<Record<string, OwnerResult | null>>({});
	// A plain, non-reactive dedupe guard for in-flight lookups; never read by the template.
	const pendingLookups: Record<string, true> = {};

	function isEmptyValue(value: unknown): boolean {
		return isUnset(value) || (Array.isArray(value) && value.length === 0);
	}

	const context = $derived({
		locale: $locale,
		defaultLocale: schema.defaultLocale,
		messages: schema.messages,
		native: nativeMessage
	});

	// What the reader chose to open; a section nobody touched opens only when it has something to say.
	let toggled = $state<Record<string, boolean>>({});
	let emptyShown = $state<Record<string, boolean>>({});

	const sections = $derived.by(() => {
		const out: { id: string; fields: MetamodelField[]; filled: number; missing: number }[] = [];
		for (const field of fields) {
			const id = field.presentation?.section ?? '';
			let section = out.find((candidate) => candidate.id === id);
			if (!section) out.push((section = { id, fields: [], filled: 0, missing: 0 }));
			section.fields.push(field);
			if (!isEmptyValue(readMetadataValue(asset.metadata, field.storage))) section.filled++;
			else if (field.required) section.missing++;
		}
		return out;
	});
	const missing = $derived(sections.reduce((total, section) => total + section.missing, 0));
	const filled = $derived(sections.reduce((total, section) => total + section.filled, 0));
	const colspan = $derived(editable ? 3 : 2);
	const RING = 2 * Math.PI * 16;

	// What points at this asset, read through each field's inverse label: the other half of its links.
	let incoming = $state<AssetReferences[]>([]);
	let incomingOpen = $state(true);

	$effect(() => {
		const id = asset.id;
		let cancelled = false;
		incoming = [];
		assetReferences(id)
			.then((found) => {
				if (cancelled) return;
				for (const group of found) group.assets.forEach(rememberAsset);
				incoming = found.filter((group) => !hiddenIncoming.includes(group.field));
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	});

	function inverse(fieldId: string): string {
		const field = schema.fields.find((candidate) => candidate.id === fieldId);
		const text = resolveMessage(field?.presentation?.inverseLabelKey, context);
		if (text) return text;
		return m.glossary_referenced_by({
			field: resolveMessage(field?.presentation?.labelKey, context) ?? fieldId
		});
	}

	async function goToMissing() {
		const section = sections.find((candidate) => candidate.missing > 0);
		const field = section?.fields.find(
			(candidate) =>
				candidate.required && isEmptyValue(readMetadataValue(asset.metadata, candidate.storage))
		);
		if (!section || !field) return;
		toggled[section.id] = true;
		if (editable && !field.system && !field.derive) startEdit(field, undefined);
		await tick();
		document
			.querySelector(`[data-governed-field="${field.id}"]`)
			?.scrollIntoView({ block: 'center', behavior: 'smooth' });
	}

	function isOpen(section: { id: string; filled: number; missing: number }): boolean {
		return toggled[section.id] ?? (sections.length === 1 || section.filled + section.missing > 0);
	}

	const neededOwnerIds = $derived.by(() => {
		const ids: string[] = [];
		for (const field of fields) {
			if (field.presentation?.control !== 'user') continue;
			const value = readMetadataValue(asset.metadata, field.storage);
			if (typeof value === 'string' && value !== '' && !ids.includes(value)) ids.push(value);
		}
		return ids;
	});

	$effect(() => {
		for (const id of neededOwnerIds) {
			if (id in resolvedOwners || pendingLookups[id]) continue;
			pendingLookups[id] = true;
			void lookupOwner(id);
		}
	});

	async function lookupOwner(id: string) {
		try {
			resolvedOwners = { ...resolvedOwners, [id]: await lookupOwnerById(id) };
		} finally {
			delete pendingLookups[id];
		}
	}

	function label(field: MetamodelField): string {
		return resolveMessage(field.presentation?.labelKey, context) ?? field.id;
	}

	function help(field: MetamodelField): string | undefined {
		return resolveMessage(field.presentation?.helpTextKey, context);
	}

	function text(value: unknown): string {
		return typeof value === 'object' ? JSON.stringify(value) : String(value);
	}

	function shown(field: MetamodelField, value: unknown): string {
		const label = valueLabel(field, value, context);
		if (label) return label;
		if (typeof value === 'boolean') return value ? m.metamodel_yes() : m.metamodel_no();
		return text(value);
	}

	function focusIf(node: HTMLElement, on: boolean) {
		if (on) node.focus();
	}

	function resetUserSearch() {
		userQuery = '';
		userResults = [];
		userFocusedIndex = -1;
		userSearching = false;
		clearTimeout(userSearchTimeout);
	}

	function startEdit(field: MetamodelField, value: unknown) {
		editingId = field.id;
		draft = draftFromValue(field, value);
		pending = '';
		errorCode = null;
		listboxOpen = false;
		resetUserSearch();
	}

	function cancel() {
		editingId = null;
		errorCode = null;
		listboxOpen = false;
		resetUserSearch();
	}

	function onKey(event: KeyboardEvent, field: MetamodelField) {
		if (event.key === 'Escape') {
			event.preventDefault();
			// First Escape closes an open panel, matching LanguageSelector; the next one cancels the row.
			if (listboxOpen) {
				listboxOpen = false;
				return;
			}
			cancel();
		} else if (event.key === 'Enter') {
			event.preventDefault();
			if (field.type === 'list' && field.itemType !== 'enum' && pending.trim() !== '') addPending();
			else void save(field);
		}
	}

	function singleSelectOptions(field: MetamodelField): { value: string; label: string }[] {
		const options = field.required ? [] : [{ value: '', label: m.metamodel_not_set() }];
		if (field.type === 'boolean') {
			return [
				...options,
				{ value: 'true', label: shown(field, true) },
				{ value: 'false', label: shown(field, false) }
			];
		}
		return [
			...options,
			...orderedValues(field.values, (value) => shown(field, value), $locale).map((value) => ({
				value,
				label: shown(field, value)
			}))
		];
	}

	function toggleListbox(event: MouseEvent) {
		event.stopPropagation();
		listboxOpen = !listboxOpen;
	}

	function selectOption(value: string) {
		draft = value;
		listboxOpen = false;
		errorCode = null;
	}

	function handleWindowClick(event: MouseEvent) {
		if (listboxOpen && !listboxRoot?.contains(event.target as Node)) listboxOpen = false;
	}

	function addPending() {
		const item = pending.trim();
		const items = Array.isArray(draft) ? draft : [];
		if (item && !items.includes(item)) draft = [...items, item];
		pending = '';
		errorCode = null;
	}

	function removeItem(item: string) {
		draft = (Array.isArray(draft) ? draft : []).filter((entry) => entry !== item);
	}

	function toggleItem(item: string, on: boolean) {
		const items = Array.isArray(draft) ? draft : [];
		draft = on ? [...items, item] : items.filter((entry) => entry !== item);
		errorCode = null;
	}

	function searchUsers(query: string) {
		userQuery = query;
		clearTimeout(userSearchTimeout);
		if (query.trim().length < 2) {
			userResults = [];
			userFocusedIndex = -1;
			return;
		}
		userSearchTimeout = setTimeout(async () => {
			userSearching = true;
			try {
				userResults = await searchUserOwners(query);
			} catch {
				userResults = [];
			} finally {
				userSearching = false;
				userFocusedIndex = -1;
			}
		}, 300);
	}

	function pickUser(owner: OwnerResult) {
		draft = owner.id;
		resolvedOwners = { ...resolvedOwners, [owner.id]: owner };
		resetUserSearch();
		errorCode = null;
	}

	const userSearchNav = createKeyboardNavigationState(
		() => userResults,
		() => userFocusedIndex,
		(i) => (userFocusedIndex = i),
		{ onSelect: pickUser, onEscape: () => (userQuery ? resetUserSearch() : cancel()) }
	);

	async function save(field: MetamodelField) {
		if (saving) return;
		if (field.type === 'list' && field.itemType !== 'enum') addPending();
		const current = readMetadataValue(asset.metadata, field.storage);
		const emptyDraft = Array.isArray(draft) ? draft.length === 0 : (draft ?? '').trim() === '';
		if (emptyDraft && isEmptyValue(current) && !field.required) {
			cancel();
			return;
		}
		const parsed = toPayload(field, draft);
		if (!parsed.ok) {
			errorCode = parsed.code;
			return;
		}
		if (sameValue(current, parsed.value)) {
			cancel();
			return;
		}
		const version = asset.version;
		if (version == null) {
			toasts.error(m.metamodel_missing_version());
			return;
		}
		saving = true;
		errorCode = null;
		try {
			const updated = await patchAssetFields(asset.id, version, { [field.id]: parsed.value });
			asset = { ...asset, ...updated };
			editingId = null;
		} catch (err) {
			if (err instanceof MetamodelHttpError && err.status === 412) {
				toasts.error(m.metamodel_conflict());
				editingId = null;
				onConflict?.();
			} else if (
				err instanceof MetamodelHttpError &&
				err.status === 400 &&
				isValidationError(err.body)
			) {
				errorCode = err.body.fields.find((v) => v.field === field.id)?.code ?? 'unknown';
			} else {
				toasts.error(m.metamodel_save_failed());
			}
		} finally {
			saving = false;
		}
	}
</script>

{#snippet ownerChip(id: string, removable: boolean)}
	{@const owner = resolvedOwners[id]}
	<span
		class="group/chip inline-flex max-w-full items-center gap-2 rounded-full bg-gray-100 py-1 pl-1 pr-2.5 dark:bg-gray-700"
	>
		{#if owner === null}
			<span
				class="flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-full bg-gray-200 text-gray-500 dark:bg-gray-600 dark:text-gray-400"
			>
				<IconifyIcon icon="material-symbols:person-off-outline-rounded" class="h-3 w-3" />
			</span>
			<span class="truncate text-sm italic text-gray-500 dark:text-gray-400">
				{m.metamodel_unknown_user()}
			</span>
		{:else}
			<Avatar name={owner?.name ?? id} profilePicture={owner?.profile_picture} size="xs" />
			<span class="truncate text-sm text-gray-900 dark:text-gray-100">{owner?.name ?? id}</span>
		{/if}
		{#if removable}
			<button
				type="button"
				onclick={() => (draft = '')}
				class="flex-shrink-0 rounded-full p-0.5 text-gray-400 opacity-0 transition-opacity group-hover/chip:opacity-100 hover:bg-gray-300 hover:text-gray-800 focus-visible:opacity-100 dark:hover:bg-gray-600 dark:hover:text-gray-100"
				aria-label={m.metamodel_list_remove({ value: owner?.name ?? id })}
			>
				<IconifyIcon icon="material-symbols:close-rounded" class="h-3.5 w-3.5" />
			</button>
		{/if}
	</span>
{/snippet}

{#snippet display(field: MetamodelField, value: unknown)}
	{#if isEmptyValue(value)}
		<span
			class="inline-flex items-center gap-1 text-sm italic {field.required
				? 'text-red-600 dark:text-red-400'
				: 'text-gray-400 dark:text-gray-500'}"
		>
			{#if field.required}
				<IconifyIcon icon="material-symbols:error-outline-rounded" class="h-3.5 w-3.5" />
			{/if}
			{m.metamodel_not_set()}
		</span>
	{:else if field.presentation?.control === 'user' && typeof value === 'string'}
		{@render ownerChip(value, false)}
	{:else if field.presentation?.control === ASSET_CONTROL}
		<AssetLinks ids={assetLinkIds(value)} />
	{:else if field.presentation?.control === 'search'}
		<SearchLinks values={Array.isArray(value) ? value.map(String) : [String(value)]} />
	{:else if Array.isArray(value)}
		<div class="flex flex-wrap gap-1.5">
			{#each value as item, i (i)}
				{#if typeof item === 'string' && isHttpUrl(item)}
					<a
						href={item.trim()}
						target="_blank"
						rel="noopener noreferrer"
						class="rounded-full bg-earthy-terracotta-100 px-2 py-0.5 text-xs whitespace-pre-wrap break-all text-earthy-terracotta-700 underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 dark:bg-earthy-terracotta-900 dark:text-earthy-terracotta-100"
					>
						{shown(field, item)}
					</a>
				{:else}
					<span
						class="rounded-full bg-earthy-terracotta-100 px-2 py-0.5 text-xs whitespace-pre-wrap break-all text-earthy-terracotta-700 dark:bg-earthy-terracotta-900 dark:text-earthy-terracotta-100"
					>
						{shown(field, item)}
					</span>
				{/if}
			{/each}
		</div>
	{:else if typeof value === 'boolean'}
		{@const href = facetHref(field, value)}
		{#if href}
			<a
				href={resolve(href as `/${string}`)}
				class="rounded-full px-2 py-1 text-sm transition-opacity hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 {valueClass(
					value
				)}"
				title={m.metamodel_filter_by({ value: shown(field, value) })}>{shown(field, value)}</a
			>
		{:else}
			<span class="rounded-full px-2 py-1 text-sm {valueClass(value)}">
				{shown(field, value)}
			</span>
		{/if}
	{:else if typeof value === 'string' && isHttpUrl(value)}
		<a
			href={value.trim()}
			target="_blank"
			rel="noopener noreferrer"
			class="rounded-full px-2 py-1 text-sm underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 {valueClass(
				value
			)}"
		>
			{shown(field, value)}
		</a>
	{:else}
		{@const href = facetHref(field, value)}
		{#if href}
			<a
				href={resolve(href as `/${string}`)}
				class="rounded-full px-2 py-1 text-sm transition-opacity hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 {valueClass(
					value
				)}"
				title={m.metamodel_filter_by({ value: shown(field, value) })}>{shown(field, value)}</a
			>
		{:else}
			<span class="rounded-full px-2 py-1 text-sm {valueClass(value)}">{shown(field, value)}</span>
		{/if}
	{/if}
{/snippet}

{#snippet userEditor(field: MetamodelField, controlId: string, described: string | undefined)}
	{@const selectedId = typeof draft === 'string' ? draft : ''}
	{#if selectedId}
		{@render ownerChip(selectedId, true)}
	{:else}
		<div class="relative">
			<input
				id={controlId}
				type="text"
				class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 pl-8 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
				placeholder={m.owners_search_users_placeholder()}
				aria-labelledby={`governed-label-${field.id}`}
				aria-describedby={described}
				aria-expanded={userQuery.trim().length >= 2}
				role="combobox"
				aria-controls={`${controlId}-listbox`}
				autocomplete="off"
				value={userQuery}
				oninput={(e) => searchUsers(e.currentTarget.value)}
				onkeydown={userSearchNav.handleKeydown}
				use:focusIf={true}
			/>
			<IconifyIcon
				icon="material-symbols:search-rounded"
				class="pointer-events-none absolute top-1/2 left-2 h-4 w-4 -translate-y-1/2 text-gray-400"
			/>
			{#if userQuery.trim().length >= 2}
				<div
					id={`${controlId}-listbox`}
					role="listbox"
					class="absolute z-10 mt-1 max-h-52 w-full min-w-64 overflow-auto rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
				>
					{#if userSearching}
						<div class="px-3 py-3 text-sm text-gray-500 dark:text-gray-400">
							{m.owners_searching()}
						</div>
					{:else if userResults.length === 0}
						<div class="px-3 py-3 text-sm text-gray-500 dark:text-gray-400">
							{m.metamodel_no_users_found()}
						</div>
					{:else}
						{#each userResults as owner, i (owner.id)}
							<button
								type="button"
								role="option"
								aria-selected={i === userFocusedIndex}
								onclick={() => pickUser(owner)}
								class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors {i ===
								userFocusedIndex
									? 'bg-gray-100 dark:bg-gray-700'
									: 'hover:bg-gray-50 dark:hover:bg-gray-700/50'}"
							>
								<Avatar name={owner.name} profilePicture={owner.profile_picture} size="xs" />
								<span class="min-w-0 flex-1 truncate text-gray-900 dark:text-gray-100"
									>{owner.name}</span
								>
								{#if owner.username}
									<span class="flex-shrink-0 text-xs text-gray-400">@{owner.username}</span>
								{/if}
							</button>
						{/each}
					{/if}
				</div>
			{/if}
		</div>
	{/if}
{/snippet}

{#snippet singleSelect(field: MetamodelField, controlId: string, described: string | undefined)}
	{@const value = typeof draft === 'string' ? draft : ''}
	{@const options = singleSelectOptions(field)}
	{@const currentLabel = options.find((o) => o.value === value)?.label ?? m.metamodel_not_set()}
	<div class="relative inline-flex w-full" bind:this={listboxRoot}>
		<button
			type="button"
			id={controlId}
			aria-haspopup="listbox"
			aria-expanded={listboxOpen}
			aria-labelledby={`governed-label-${field.id} ${controlId}`}
			aria-describedby={described}
			onclick={toggleListbox}
			onkeydown={(e) => onKey(e, field)}
			class="flex w-full items-center justify-between gap-2 rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-left text-sm text-gray-900 focus:border-transparent focus:ring-2 focus:ring-earthy-terracotta-500 focus:outline-none dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
			use:focusIf={true}
		>
			<span class="min-w-0 truncate">{currentLabel}</span>
			<IconifyIcon
				icon="material-symbols:keyboard-arrow-down"
				class="h-4 w-4 shrink-0 text-gray-500 transition-transform dark:text-gray-400 {listboxOpen
					? 'rotate-180'
					: ''}"
			/>
		</button>
		{#if listboxOpen}
			<div
				role="listbox"
				aria-labelledby={`governed-label-${field.id}`}
				class="absolute top-full left-0 z-10 mt-1 max-h-60 w-full min-w-max overflow-y-auto overscroll-contain rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
			>
				{#each options as option (option.value)}
					<button
						type="button"
						role="option"
						aria-selected={option.value === value}
						onclick={(e) => {
							e.stopPropagation();
							selectOption(option.value);
						}}
						class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors {option.value ===
						value
							? 'font-medium text-earthy-terracotta-700 dark:text-earthy-terracotta-500'
							: 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'}"
					>
						<span>{option.label}</span>
						{#if option.value === value}
							<span
								class="h-1.5 w-1.5 rounded-full bg-earthy-terracotta-700 dark:bg-earthy-terracotta-500"
								aria-hidden="true"
							></span>
						{/if}
					</button>
				{/each}
			</div>
		{/if}
	</div>
{/snippet}

{#snippet editor(field: MetamodelField)}
	{@const controlId = `governed-${field.id}`}
	{@const rules = field.validation ?? {}}
	{@const scalar = typeof draft === 'string' ? draft : ''}
	{@const described = errorCode ? `governed-error-${field.id}` : undefined}
	<div class="flex items-start gap-2">
		<div class="min-w-0 flex-1">
			{#if field.presentation?.control === 'user'}
				{@render userEditor(field, controlId, described)}
			{:else if field.presentation?.control === ASSET_CONTROL}
				<AssetLinkPicker
					ids={assetLinkIds(draft)}
					multiple={field.type === 'list'}
					exclude={asset.id}
					assetTypes={field.presentation?.targetAssetTypes}
					inputId={controlId}
					labelledby={`governed-label-${field.id}`}
					describedby={described}
					onchange={(ids) => {
						draft = field.type === 'list' ? ids : (ids[0] ?? '');
						errorCode = null;
					}}
					onescape={cancel}
				/>
			{:else if field.type === 'integer' || field.type === 'number'}
				<input
					id={controlId}
					type="number"
					step={field.type === 'integer' ? '1' : 'any'}
					min={rules.minimum}
					max={rules.maximum}
					class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
					aria-labelledby={`governed-label-${field.id}`}
					aria-invalid={errorCode ? true : undefined}
					aria-describedby={described}
					value={scalar}
					oninput={(e) => (draft = e.currentTarget.value)}
					onkeydown={(e) => onKey(e, field)}
					use:focusIf={true}
				/>
			{:else if field.type === 'enum' || field.type === 'boolean'}
				{@render singleSelect(field, controlId, described)}
			{:else if field.type === 'date'}
				<input
					id={controlId}
					type="date"
					class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
					aria-labelledby={`governed-label-${field.id}`}
					aria-invalid={errorCode ? true : undefined}
					aria-describedby={described}
					value={scalar}
					oninput={(e) => (draft = e.currentTarget.value)}
					onkeydown={(e) => onKey(e, field)}
					use:focusIf={true}
				/>
			{:else if field.type === 'url'}
				<input
					id={controlId}
					type="url"
					inputmode="url"
					placeholder="https://"
					maxlength={rules.maxLength}
					class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
					aria-labelledby={`governed-label-${field.id}`}
					aria-invalid={errorCode ? true : undefined}
					aria-describedby={described}
					value={scalar}
					oninput={(e) => (draft = e.currentTarget.value)}
					onkeydown={(e) => onKey(e, field)}
					use:focusIf={true}
				/>
			{:else if field.type === 'list' && field.itemType === 'enum'}
				<div
					role="group"
					aria-labelledby={`governed-label-${field.id}`}
					aria-describedby={described}
					class="flex flex-wrap gap-x-4 gap-y-1"
				>
					{#each orderedValues(field.values, (value) => shown(field, value), $locale) as option, i (option)}
						<label class="flex items-center gap-1.5 text-sm text-gray-700 dark:text-gray-300">
							<input
								type="checkbox"
								class="rounded border-gray-300 dark:border-gray-600"
								checked={Array.isArray(draft) && draft.includes(option)}
								onchange={(e) => toggleItem(option, e.currentTarget.checked)}
								onkeydown={(e) => onKey(e, field)}
								use:focusIf={i === 0}
							/>
							{shown(field, option)}
						</label>
					{/each}
				</div>
			{:else if field.type === 'list'}
				<div class="flex flex-wrap items-center gap-1.5">
					{#each Array.isArray(draft) ? draft : [] as item (item)}
						<span
							class="inline-flex items-center gap-1 rounded-full bg-earthy-terracotta-100 px-2 py-0.5 text-xs whitespace-pre-wrap break-all text-earthy-terracotta-700 dark:bg-earthy-terracotta-900 dark:text-earthy-terracotta-100"
						>
							{item}
							<button
								type="button"
								class="rounded hover:text-earthy-terracotta-900 dark:hover:text-white"
								aria-label={m.metamodel_list_remove({ value: item })}
								onclick={() => removeItem(item)}
							>
								<IconifyIcon icon="material-symbols:close-rounded" class="w-3.5 h-3.5" />
							</button>
						</span>
					{/each}
				</div>
				<input
					id={controlId}
					type={field.itemType === 'integer' || field.itemType === 'number'
						? 'number'
						: field.itemType === 'url'
							? 'url'
							: 'text'}
					step={field.itemType === 'integer' ? '1' : 'any'}
					inputmode={field.itemType === 'url' ? 'url' : undefined}
					placeholder={field.itemType === 'url' ? 'https://' : m.metamodel_list_placeholder()}
					class="mt-1.5 w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
					aria-labelledby={`governed-label-${field.id}`}
					aria-invalid={errorCode ? true : undefined}
					aria-describedby={described}
					bind:value={pending}
					onkeydown={(e) => onKey(e, field)}
					use:focusIf={true}
				/>
			{:else}
				<input
					id={controlId}
					type="text"
					maxlength={rules.maxLength}
					class="w-full rounded border border-earthy-terracotta-500 bg-white px-2 py-1.5 text-sm text-gray-900 focus:ring-1 focus:ring-earthy-terracotta-600 dark:border-earthy-terracotta-700 dark:bg-gray-800 dark:text-gray-100"
					aria-labelledby={`governed-label-${field.id}`}
					aria-invalid={errorCode ? true : undefined}
					aria-describedby={described}
					value={scalar}
					oninput={(e) => (draft = e.currentTarget.value)}
					onkeydown={(e) => onKey(e, field)}
					use:focusIf={true}
				/>
			{/if}
			{#if errorCode}
				<p
					id={`governed-error-${field.id}`}
					role="alert"
					class="mt-1 text-xs text-red-600 dark:text-red-400"
				>
					{violationMessage(errorCode)}
				</p>
			{/if}
		</div>
		<div class="flex flex-shrink-0 items-center gap-1">
			<button
				type="button"
				onclick={() => save(field)}
				disabled={saving}
				class="rounded p-1.5 text-green-600 transition-colors hover:bg-green-50 disabled:opacity-50 dark:text-green-500 dark:hover:bg-green-900/20"
				title={m.common_save()}
				aria-label={m.common_save()}
			>
				<IconifyIcon icon="material-symbols:check-rounded" class="h-5 w-5" />
			</button>
			<button
				type="button"
				onclick={cancel}
				disabled={saving}
				class="rounded p-1.5 text-gray-500 transition-colors hover:bg-gray-100 dark:hover:bg-gray-700"
				title={m.common_cancel()}
				aria-label={m.common_cancel()}
			>
				<IconifyIcon icon="material-symbols:close-rounded" class="h-5 w-5" />
			</button>
		</div>
	</div>
{/snippet}

<svelte:window onclick={handleWindowClick} />

{#snippet row(field: MetamodelField, value: unknown)}
	{@const helpText = help(field)}
	{@const unmet = field.required && isEmptyValue(value)}
	<div
		class="group -mx-2 grid grid-cols-1 gap-x-6 gap-y-1 rounded-md px-2 py-2.5 transition-colors sm:grid-cols-[15rem_minmax(0,1fr)] {unmet
			? 'bg-red-50/70 dark:bg-red-900/10'
			: 'hover:bg-gray-50 dark:hover:bg-gray-700/30'}"
		data-governed-field={field.id}
	>
		<div>
			<div
				id={`governed-label-${field.id}`}
				class="flex items-baseline gap-1 text-sm font-medium text-gray-700 dark:text-gray-300"
			>
				<span>{label(field)}</span>
				{#if field.required}
					<span class="text-red-500" aria-hidden="true">*</span>
					<span class="sr-only">({m.metamodel_required()})</span>
				{/if}
				{#if helpText}
					<span class="text-gray-400 dark:text-gray-500" title={helpText} aria-hidden="true">
						<IconifyIcon icon="mdi:information-outline" class="h-3.5 w-3.5" />
					</span>
				{/if}
			</div>
			{#if editingId === field.id}
				<div class="mt-0.5 flex items-center gap-1 text-xs text-gray-400 dark:text-gray-500">
					<IconifyIcon icon={typeIcon(field)} class="h-3.5 w-3.5" />
					{typeLabel(field)}
				</div>
			{/if}
		</div>
		<div class="min-w-0 text-sm">
			{#if editingId === field.id}
				{@render editor(field)}
			{:else}
				<div class="inline-flex items-center gap-1.5">
					{@render display(field, value)}
					{#if field.derive}
						{@const source = fields.find((candidate) => candidate.id === field.derive?.from)}
						<span
							class="flex-shrink-0 text-gray-400 dark:text-gray-500"
							title={m.metamodel_derived_from({
								field: source ? label(source) : field.derive.from
							})}
						>
							<IconifyIcon icon="material-symbols:function" class="h-4 w-4" />
						</span>
					{:else if field.system}
						<span
							class="flex-shrink-0 text-gray-400 dark:text-gray-500"
							title={m.metamodel_system_field()}
						>
							<IconifyIcon icon="material-symbols:smart-toy-outline-rounded" class="h-4 w-4" />
						</span>
					{:else if editable}
						<button
							type="button"
							onclick={() => startEdit(field, value)}
							disabled={saving}
							class="flex-shrink-0 rounded p-1.5 text-gray-400 transition-all group-hover:opacity-100 hover:bg-gray-100 {unmet
								? ''
								: 'opacity-0'} hover:text-earthy-terracotta-700 focus-visible:opacity-100 dark:hover:bg-gray-700 dark:hover:text-earthy-terracotta-500"
							title={m.common_edit()}
							aria-label={`${m.common_edit()}: ${label(field)}`}
						>
							<IconifyIcon icon="material-symbols:edit-outline-rounded" class="h-4 w-4" />
						</button>
					{/if}
				</div>
			{/if}
		</div>
	</div>
{/snippet}

<tr data-governed-summary>
	<td {colspan} class="border-b border-gray-200 p-0 dark:border-gray-700">
		<div class="flex flex-wrap items-center gap-4 px-5 py-4">
			<svg viewBox="0 0 40 40" class="h-11 w-11 flex-shrink-0 -rotate-90" aria-hidden="true">
				<circle
					cx="20"
					cy="20"
					r="16"
					fill="none"
					stroke-width="4"
					class="stroke-gray-200 dark:stroke-gray-700"
				/>
				<circle
					cx="20"
					cy="20"
					r="16"
					fill="none"
					stroke-width="4"
					stroke-linecap="round"
					stroke-dasharray={RING}
					stroke-dashoffset={RING * (1 - (fields.length ? filled / fields.length : 0))}
					class="transition-all {missing > 0
						? 'stroke-amber-500'
						: 'stroke-green-600 dark:stroke-green-500'}"
				/>
			</svg>
			<div class="min-w-0">
				<p class="text-sm font-medium text-gray-900 dark:text-gray-100">
					{m.metamodel_fields_filled({ filled, total: fields.length })}
				</p>
				<p
					class="flex items-center gap-1 text-xs {missing > 0
						? 'text-gray-500 dark:text-gray-400'
						: 'text-green-700 dark:text-green-500'}"
				>
					{#if missing > 0}
						{m.metamodel_required_missing({ count: missing })}
					{:else}
						<IconifyIcon icon="material-symbols:check-circle-outline-rounded" class="h-3.5 w-3.5" />
						{m.metamodel_required_done()}
					{/if}
				</p>
			</div>
			{#if missing > 0}
				<button
					type="button"
					onclick={goToMissing}
					data-governed-missing
					class="ml-auto inline-flex items-center gap-1.5 rounded-full bg-amber-100 px-3 py-1.5 text-sm font-medium text-amber-900 transition-colors hover:bg-amber-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-500 dark:bg-amber-900/40 dark:text-amber-100 dark:hover:bg-amber-900/60"
				>
					{m.metamodel_fill_required()}
					<IconifyIcon icon="material-symbols:arrow-forward-rounded" class="h-4 w-4" />
				</button>
			{/if}
		</div>
	</td>
</tr>
{#each sections as section (section.id)}
	{@const open = isOpen(section)}
	{@const hidden = section.fields.length - section.filled - section.missing}
	{#if sections.length > 1}
		<tr data-governed-section={section.id}>
			<td {colspan} class="border-b border-gray-200 p-0 dark:border-gray-700">
				<button
					type="button"
					aria-expanded={open}
					onclick={() => (toggled[section.id] = !open)}
					class="flex w-full items-center gap-3 px-5 py-3 text-left transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-earthy-terracotta-600 dark:hover:bg-gray-700/30"
				>
					<IconifyIcon
						icon="material-symbols:chevron-right-rounded"
						class="h-5 w-5 flex-shrink-0 text-gray-400 transition-transform {open
							? 'rotate-90'
							: ''}"
					/>
					<span class="text-sm font-semibold text-gray-800 dark:text-gray-100">
						{section.id ? sectionLabel(section.id, context) : m.metamodel_other_section()}
					</span>
					{#if section.missing > 0}
						<span
							class="rounded-full bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 ring-1 ring-red-200 ring-inset dark:bg-red-900/20 dark:text-red-300 dark:ring-red-900"
						>
							{m.metamodel_pending({ count: section.missing })}
						</span>
					{/if}
					<span
						class="ml-auto flex flex-shrink-0 items-center gap-2 text-xs text-gray-500 tabular-nums dark:text-gray-400"
					>
						<span
							class="h-1.5 w-20 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700"
							aria-hidden="true"
						>
							<span
								class="block h-full rounded-full bg-earthy-terracotta-600 transition-all"
								style="width: {(section.filled / section.fields.length) * 100}%"
							></span>
						</span>
						{section.filled}/{section.fields.length}
					</span>
				</button>
			</td>
		</tr>
	{/if}
	{#if open}
		<tr>
			<td {colspan} class="border-b border-gray-200 px-5 pt-1 pb-3 dark:border-gray-700">
				{#each section.fields as field (field.id)}
					{@const value = readMetadataValue(asset.metadata, field.storage)}
					{#if !isEmptyValue(value) || field.required || editingId === field.id || emptyShown[section.id]}
						{@render row(field, value)}
					{/if}
				{/each}
				{#if hidden > 0}
					<button
						type="button"
						data-governed-empty={section.id}
						aria-expanded={!!emptyShown[section.id]}
						onclick={() => (emptyShown[section.id] = !emptyShown[section.id])}
						class="mt-1 inline-flex items-center gap-1.5 rounded-full px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-earthy-terracotta-600 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100"
					>
						<IconifyIcon
							icon={emptyShown[section.id]
								? 'material-symbols:visibility-off-outline-rounded'
								: 'material-symbols:add-rounded'}
							class="h-4 w-4"
						/>
						{emptyShown[section.id]
							? m.metamodel_hide_empty()
							: m.metamodel_show_empty({ count: hidden })}
					</button>
				{/if}
			</td>
		</tr>
	{/if}
{/each}
{#if incoming.length > 0}
	<tr data-governed-incoming>
		<td {colspan} class="border-b border-gray-200 p-0 dark:border-gray-700">
			<button
				type="button"
				aria-expanded={incomingOpen}
				onclick={() => (incomingOpen = !incomingOpen)}
				class="flex w-full items-center gap-3 px-5 py-3 text-left transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-earthy-terracotta-600 dark:hover:bg-gray-700/30"
			>
				<IconifyIcon
					icon="material-symbols:chevron-right-rounded"
					class="h-5 w-5 flex-shrink-0 text-gray-400 transition-transform {incomingOpen
						? 'rotate-90'
						: ''}"
				/>
				<span class="text-sm font-semibold text-gray-800 dark:text-gray-100">
					{m.metamodel_incoming_links()}
				</span>
				<span class="ml-auto text-xs text-gray-500 tabular-nums dark:text-gray-400">
					{incoming.reduce((total, group) => total + group.assets.length, 0)}
				</span>
			</button>
		</td>
	</tr>
	{#if incomingOpen}
		<tr>
			<td {colspan} class="border-b border-gray-200 px-5 pt-1 pb-3 dark:border-gray-700">
				{#each incoming as group (group.field)}
					<div
						class="-mx-2 grid grid-cols-1 gap-x-6 gap-y-1 rounded-md px-2 py-2.5 sm:grid-cols-[15rem_minmax(0,1fr)]"
						data-asset-references={group.field}
					>
						<div class="text-sm font-medium text-gray-700 dark:text-gray-300">
							{inverse(group.field)}
						</div>
						<div class="min-w-0 text-sm">
							<AssetLinks ids={group.assets.map((linked) => linked.id)} />
						</div>
					</div>
				{/each}
			</td>
		</tr>
	{/if}
{/if}
