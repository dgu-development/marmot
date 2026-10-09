<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { fetchApi } from '$lib/api';
	import { toasts, parseApiError, isLimitExceeded } from '$lib/stores/toast';
	import IconifyIcon from '@iconify/svelte';
	import Icon from '$components/ui/Icon.svelte';
	import StepperPage from '$components/ui/StepperPage.svelte';
	import TagsInput from '$components/shared/TagsInput.svelte';
	import DomainSelect from '$components/domain/DomainSelect.svelte';
	import { providerIconMap, typeIconMap } from '$lib/iconloader';
	import { m } from '$lib/paraglide/messages';
	import { locale } from '$lib/i18n';
	import { fetchMetamodel } from '$lib/metamodel/api';
	import { nativeMessage } from '$lib/metamodel/i18n';
	import { resolveMessage, valueLabel } from '$lib/metamodel/labels';
	import { ALPHABETICAL_FROM, groupedValues } from '$lib/metamodel/values';
	import type { MetamodelSchema } from '$lib/metamodel/types';

	let name = $state('');
	let assetType = $state('');
	let providers = $state<string[]>([]);
	let origin = $state<'technical' | 'manual'>('technical');
	let manualType = $state('');
	let schema = $state<MetamodelSchema>();
	const typeField = $derived(
		schema?.enabled
			? schema.fields.find((f) => f.id === 'asset_type' && f.presentation?.manual)
			: undefined
	);
	const manual = $derived(typeField?.presentation?.manual);
	const isManual = $derived(origin === 'manual' && !!manual);
	const labelContext = $derived({
		locale: $locale,
		defaultLocale: schema?.defaultLocale ?? 'en',
		messages: schema?.messages,
		native: nativeMessage
	});
	const manualLabel = (value: string) =>
		(typeField && valueLabel(typeField, value, labelContext)) ?? value;

	let typeQuery = $state('');
	const fold = (text: string) => text.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();
	const typeGroups = $derived.by(() => {
		if (!manual || !typeField || !schema) return groupedValues([], [], '');
		const query = fold(typeQuery.trim());
		const matching = manual.values.filter((value) => fold(manualLabel(value)).includes(query));
		return groupedValues(matching, schema.fields, typeField.id);
	});
	const groupLabel = (group: string) =>
		(typeGroups.by && valueLabel(typeGroups.by, group, labelContext)) ?? group;
	// The profile describes a value under the key of its label plus `.help`.
	const manualHelp = $derived(
		resolveMessage(
			manualType && `${typeField?.presentation?.valueLabelKeys?.[manualType]}.help`,
			labelContext
		)
	);

	$effect(() => {
		fetchMetamodel()
			.then((loaded) => (schema = loaded))
			.catch(() => {});
	});

	// The server derives an asset's identifier from its type, its first provider and its name, so
	// the same three cannot be created twice; the same name alone can, and is worth a look.
	interface Existing {
		id: string;
		name: string;
		type: string;
		providers: string[];
		mrn: string;
	}
	let sameName = $state<Existing[]>([]);
	let taken = $state<Existing | null>(null);
	const identity = $derived(
		isManual && manual
			? { type: manual.type, provider: manual.provider }
			: assetType.trim() && providers.length > 0
				? { type: assetType.trim(), provider: providers[0] }
				: null
	);
	function assetPath(mrn: string) {
		const parts = mrn.replace('mrn://', '').split('/');
		return `/discover/${parts[0]}/${parts[1]}/${encodeURIComponent(parts.slice(2).join('/'))}`;
	}

	$effect(() => {
		const wanted = name.trim();
		const who = identity;
		sameName = [];
		taken = null;
		if (wanted.length < 2) return;
		const controller = new AbortController();
		const timer = setTimeout(async () => {
			try {
				const found = await fetchApi(`/assets/search?q=${encodeURIComponent(wanted)}&limit=20`, {
					signal: controller.signal
				});
				if (found.ok) {
					const body = await found.json();
					sameName = ((body.assets ?? []) as Existing[]).filter(
						(asset) => asset.name?.toLowerCase() === wanted.toLowerCase()
					);
				}
				if (who) {
					const hit = await fetchApi(
						`/assets/lookup/${encodeURIComponent(who.type)}/${encodeURIComponent(who.provider)}/${encodeURIComponent(wanted)}`,
						{ signal: controller.signal }
					);
					taken = hit.ok ? await hit.json() : null;
				}
			} catch {
				// A lookup that fails must not block the form: the server still refuses a duplicate.
			}
		}, 300);
		return () => {
			clearTimeout(timer);
			controller.abort();
		};
	});

	function nestedMetadata(storage: string, value: string): Record<string, unknown> {
		const keys = storage.replace(/^metadata\./, '').split('.');
		let nested: Record<string, unknown> = { [keys[keys.length - 1]]: value };
		for (let i = keys.length - 2; i >= 0; i--) nested = { [keys[i]]: nested };
		return nested;
	}
	let userDescription = $state('');
	let tags = $state<string[]>([]);
	let domainId = $state('');
	let saving = $state(false);
	let error = $state<string | null>(null);
	let currentStep = $state(1);
	const stepperSteps = [
		{ title: m.assetnew_step_basic_info(), icon: 'material-symbols:info-outline' },
		{ title: m.assetnew_step_type_providers(), icon: 'material-symbols:category' },
		{ title: m.assetnew_step_details(), icon: 'material-symbols:description' }
	];

	// Field-level validation
	let fieldErrors = $state<Record<string, string>>({});
	let touched = $state<Record<string, boolean>>({});

	function validateName(value: string): string | null {
		if (!value.trim()) return m.assetnew_error_name_required();
		if (value.trim().length < 2) return m.assetnew_error_name_too_short();
		// A business asset is named by people; the server derives its identifier from the name.
		if (!isManual && !/^[a-zA-Z0-9_\-.]+$/.test(value.trim()))
			return m.assetnew_error_name_charset();
		return null;
	}

	function validateType(value: string): string | null {
		if (!value.trim()) return m.assetnew_error_type_required();
		return null;
	}

	function validateProviders(value: string[]): string | null {
		if (value.length === 0) return m.assetnew_error_provider_required();
		return null;
	}

	function validateField(field: string) {
		touched[field] = true;
		if (field === 'name') {
			const err = validateName(name);
			if (err) fieldErrors['name'] = err;
			else delete fieldErrors['name'];
		} else if (field === 'type') {
			const err = validateType(assetType);
			if (err) fieldErrors['type'] = err;
			else delete fieldErrors['type'];
		} else if (field === 'providers') {
			const err = validateProviders(providers);
			if (err) fieldErrors['providers'] = err;
			else delete fieldErrors['providers'];
		}
		fieldErrors = { ...fieldErrors };
	}

	function clearFieldError(field: string) {
		delete fieldErrors[field];
		fieldErrors = { ...fieldErrors };
	}

	// Search/dropdown state
	let typeSearch = $state('');
	let providerSearch = $state('');
	let showTypeDropdown = $state(false);
	let showProviderDropdown = $state(false);
	let selectedTypeIndex = $state(-1);
	let selectedProviderIndex = $state(-1);
	let typeDropdownElement = $state<HTMLDivElement>();
	let providerDropdownElement = $state<HTMLDivElement>();

	let canProceedToStep2 = $derived(validateName(name) === null);
	let canProceedToStep3 = $derived(
		isManual
			? manualType !== ''
			: validateType(assetType) === null && validateProviders(providers) === null
	);

	function canNavigateToStep(stepNumber: number): boolean {
		if (stepNumber === 2) return canProceedToStep2;
		if (stepNumber === 3) return canProceedToStep3;
		return false;
	}

	// Get unique suggestions with proper casing
	let filteredTypes = $derived(
		Object.keys(typeIconMap)
			.filter((type) =>
				typeIconMap[type].displayName.toLowerCase().includes(typeSearch.toLowerCase())
			)
			.map((type) => ({ key: type, display: typeIconMap[type].displayName }))
	);

	let filteredProviders = $derived(
		Object.keys(providerIconMap)
			.filter((provider) =>
				providerIconMap[provider].displayName.toLowerCase().includes(providerSearch.toLowerCase())
			)
			.map((provider) => ({ key: provider, display: providerIconMap[provider].displayName }))
	);

	// Reset selected index when filtered list changes
	$effect(() => {
		if (filteredTypes) selectedTypeIndex = -1;
	});
	$effect(() => {
		if (filteredProviders) selectedProviderIndex = -1;
	});

	function selectType(typeObj: { key: string; display: string }) {
		assetType = typeObj.display;
		typeSearch = typeObj.display;
		showTypeDropdown = false;
	}

	function handleTypeKeydown(event: KeyboardEvent) {
		if (!showTypeDropdown && event.key !== 'Escape') {
			showTypeDropdown = true;
		}

		if (event.key === 'ArrowDown') {
			event.preventDefault();
			selectedTypeIndex = Math.min(selectedTypeIndex + 1, filteredTypes.length - 1);
			scrollToSelectedType();
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			selectedTypeIndex = Math.max(selectedTypeIndex - 1, -1);
			scrollToSelectedType();
		} else if (event.key === 'Enter') {
			event.preventDefault();
			if (selectedTypeIndex >= 0 && filteredTypes[selectedTypeIndex]) {
				selectType(filteredTypes[selectedTypeIndex]);
			} else if (typeSearch.trim()) {
				assetType = typeSearch.trim();
				showTypeDropdown = false;
			}
		} else if (event.key === 'Escape') {
			event.preventDefault();
			showTypeDropdown = false;
			selectedTypeIndex = -1;
		}
	}

	function scrollToSelectedType() {
		if (typeDropdownElement && selectedTypeIndex >= 0) {
			const buttons = typeDropdownElement.querySelectorAll('button');
			if (buttons[selectedTypeIndex]) {
				buttons[selectedTypeIndex].scrollIntoView({ block: 'nearest', behavior: 'smooth' });
			}
		}
	}

	function handleProviderKeydown(event: KeyboardEvent) {
		if (!showProviderDropdown && event.key !== 'Escape') {
			showProviderDropdown = true;
		}

		if (event.key === 'ArrowDown') {
			event.preventDefault();
			selectedProviderIndex = Math.min(selectedProviderIndex + 1, filteredProviders.length - 1);
			scrollToSelectedProvider();
		} else if (event.key === 'ArrowUp') {
			event.preventDefault();
			selectedProviderIndex = Math.max(selectedProviderIndex - 1, -1);
			scrollToSelectedProvider();
		} else if (event.key === 'Enter') {
			event.preventDefault();
			if (selectedProviderIndex >= 0 && filteredProviders[selectedProviderIndex]) {
				toggleProvider(filteredProviders[selectedProviderIndex]);
				selectedProviderIndex = -1;
			} else if (providerSearch.trim()) {
				if (!providers.includes(providerSearch.trim())) {
					providers = [...providers, providerSearch.trim()];
				}
				providerSearch = '';
			}
		} else if (event.key === 'Escape') {
			event.preventDefault();
			showProviderDropdown = false;
			selectedProviderIndex = -1;
		}
	}

	function scrollToSelectedProvider() {
		if (providerDropdownElement && selectedProviderIndex >= 0) {
			const buttons = providerDropdownElement.querySelectorAll('button');
			if (buttons[selectedProviderIndex]) {
				buttons[selectedProviderIndex].scrollIntoView({ block: 'nearest', behavior: 'smooth' });
			}
		}
	}

	function toggleProvider(providerObj: { key: string; display: string }) {
		const displayName = providerObj.display;
		if (providers.includes(displayName)) {
			providers = providers.filter((p) => p !== displayName);
		} else {
			providers = [...providers, displayName];
		}
		providerSearch = '';
	}

	function removeProvider(provider: string) {
		providers = providers.filter((p) => p !== provider);
	}

	async function handleSave() {
		if (
			isManual
				? !name.trim() || !manualType
				: !name.trim() || !assetType.trim() || providers.length === 0
		) {
			error = m.assetnew_error_missing_required();
			return;
		}

		try {
			saving = true;
			error = null;

			const payload: Record<string, unknown> =
				isManual && manual && typeField
					? {
							name: name.trim(),
							type: manual.type,
							providers: [manual.provider],
							metadata: nestedMetadata(typeField.storage, manualType)
						}
					: { name: name.trim(), type: assetType.trim(), providers };

			if (userDescription.trim()) {
				payload.user_description = userDescription.trim();
			}
			if (tags.length > 0) {
				payload.tags = tags;
			}

			const target = domainId ? `?domain_id=${encodeURIComponent(domainId)}` : '';
			const response = await fetchApi(`/assets/${target}`, {
				method: 'POST',
				body: JSON.stringify(payload)
			});

			if (!response.ok) {
				const info = await parseApiError(response);
				if (isLimitExceeded(info)) toasts.warning(info.message);
				throw new Error(info.message);
			}

			const data = await response.json();
			// URL format: /discover/type/provider/name
			const type = encodeURIComponent(data.type.toLowerCase());
			const provider = encodeURIComponent(data.providers[0].toLowerCase());
			const assetName = encodeURIComponent(data.name);
			goto(resolve(`/discover/${type}/${provider}/${assetName}`));
		} catch (err) {
			error = err instanceof Error ? err.message : m.assetnew_error_create();
		} finally {
			saving = false;
		}
	}

	function handleNextStep() {
		if (currentStep === 1) {
			validateField('name');
			const nameErr = validateName(name);
			if (nameErr) {
				error = nameErr;
				return;
			}
			error = null;
			currentStep++;
			return;
		}

		if (currentStep === 2 && isManual) {
			if (!manualType) {
				error = m.assetnew_error_type_required();
				return;
			}
			error = null;
			currentStep++;
			return;
		}

		if (currentStep === 2) {
			validateField('type');
			validateField('providers');
			const typeErr = validateType(assetType);
			const providersErr = validateProviders(providers);
			if (typeErr) {
				error = typeErr;
				return;
			}
			if (providersErr) {
				error = providersErr;
				return;
			}
			error = null;
			currentStep++;
			return;
		}
	}
</script>

<StepperPage
	title={m.assetnew_create_asset()}
	steps={stepperSteps}
	{currentStep}
	onBack={() => goto(resolve('/discover'))}
	onCancel={() => goto(resolve('/discover'))}
	onPrevious={() => currentStep--}
	onNext={handleNextStep}
	onSave={handleSave}
	canProceed={taken
		? false
		: currentStep === 1
			? canProceedToStep2
			: currentStep === 2
				? canProceedToStep3
				: isManual
					? !!name.trim() && !!manualType
					: !!name.trim() && !!assetType.trim() && providers.length > 0}
	{saving}
	saveLabel={m.assetnew_create_asset()}
	savingLabel={m.assetnew_creating()}
	{error}
	{canNavigateToStep}
	onStepClick={(step) => (currentStep = step)}
>
	{#snippet banner()}
		<div
			class="mb-6 bg-gradient-to-r from-green-50 to-emerald-50 dark:from-green-900/20 dark:to-emerald-900/20 border border-green-200 dark:border-green-800/50 rounded-lg p-5"
		>
			<div class="flex items-start gap-3">
				<IconifyIcon
					icon="material-symbols:auto-awesome"
					class="h-5 w-5 text-green-600 dark:text-green-400 mt-0.5 flex-shrink-0"
				/>
				<div class="flex-1">
					<h4 class="text-sm font-semibold text-green-900 dark:text-green-100">
						{m.assetnew_banner_title()}
					</h4>
					<p class="text-sm text-green-700 dark:text-green-300 mt-1">
						{m.assetnew_banner_description()}
					</p>
					<a
						href={resolve('/runs?tab=pipelines')}
						class="inline-flex items-center gap-2 mt-3 px-4 py-2 bg-green-600 hover:bg-green-700 text-white text-sm font-medium rounded-lg shadow-sm transition-all hover:shadow-md"
					>
						<IconifyIcon icon="material-symbols:rocket-launch" class="h-4 w-4" />
						{m.assetnew_banner_cta()}
					</a>
				</div>
			</div>
		</div>
	{/snippet}

	<!-- Step 1: Basic Information -->
	{#if currentStep === 1}
		<div
			class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-6"
		>
			<h3 class="text-base font-semibold text-gray-900 dark:text-gray-100 mb-4 flex items-center">
				<IconifyIcon
					icon="material-symbols:info-outline"
					class="h-5 w-5 mr-2 text-earthy-terracotta-600"
				/>
				{m.assetnew_basic_info_heading()}
			</h3>
			{#if manual}
				<fieldset class="mb-6">
					<legend class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
						{m.assetnew_origin_label()}
					</legend>
					<div class="grid gap-3 sm:grid-cols-2">
						{#each [{ id: 'technical', title: m.assetnew_origin_technical(), hint: m.assetnew_origin_technical_hint(), icon: 'material-symbols:database-outline' }, { id: 'manual', title: m.assetnew_origin_manual(), hint: m.assetnew_origin_manual_hint(), icon: 'material-symbols:account-tree-outline' }] as option (option.id)}
							<label
								class="flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-earthy-terracotta-600 {origin ===
								option.id
									? 'border-earthy-terracotta-600 bg-earthy-terracotta-50 dark:bg-earthy-terracotta-900/20'
									: 'border-gray-300 dark:border-gray-600'}"
							>
								<input
									type="radio"
									name="asset-origin"
									class="sr-only"
									value={option.id}
									checked={origin === option.id}
									onchange={() => {
										origin = option.id as 'technical' | 'manual';
										if (touched['name']) validateField('name');
									}}
								/>
								<IconifyIcon icon={option.icon} class="mt-0.5 h-5 w-5 flex-shrink-0" />
								<span>
									<span class="block text-sm font-medium text-gray-900 dark:text-gray-100"
										>{option.title}</span
									>
									<span class="block text-xs text-gray-500 dark:text-gray-400">{option.hint}</span>
								</span>
							</label>
						{/each}
					</div>
				</fieldset>
			{/if}
			<div>
				<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
					{m.assetnew_name_label()} <span class="text-red-500">*</span>
				</label>
				<input
					type="text"
					bind:value={name}
					placeholder={m.assetnew_name_placeholder()}
					oninput={() => {
						if (touched['name']) validateField('name');
						else clearFieldError('name');
					}}
					onblur={() => validateField('name')}
					onkeydown={(e) => {
						if (e.key === 'Enter' && canProceedToStep2) {
							e.preventDefault();
							handleNextStep();
						}
					}}
					class="w-full px-4 py-2.5 border rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-earthy-terracotta-600 focus:border-transparent transition-all {fieldErrors[
						'name'
					] && touched['name']
						? 'border-red-500 dark:border-red-500'
						: 'border-gray-300 dark:border-gray-600'}"
					required
				/>
				{#if fieldErrors['name'] && touched['name']}
					<p class="mt-1.5 text-sm text-red-600 dark:text-red-400 flex items-center">
						<IconifyIcon icon="material-symbols:error" class="h-4 w-4 mr-1 flex-shrink-0" />
						{fieldErrors['name']}
					</p>
				{:else}
					<p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
						{m.assetnew_name_hint()}
					</p>
				{/if}
				{#if taken}
					<p class="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">
						{m.assetnew_name_taken()}
						<a class="font-medium underline" href={resolve(assetPath(taken.mrn) as never)}
							>{taken.name}</a
						>
					</p>
				{:else if sameName.length > 0}
					<div class="mt-2 text-sm text-amber-700 dark:text-amber-300" role="status">
						<p>{m.assetnew_name_same()}</p>
						<ul class="mt-1 space-y-0.5">
							{#each sameName.slice(0, 5) as asset (asset.id)}
								<li>
									<a class="font-medium underline" href={resolve(assetPath(asset.mrn) as never)}
										>{asset.name}</a
									>
									<span class="text-gray-500 dark:text-gray-400">
										· {asset.type} · {asset.providers?.join(', ')}
									</span>
								</li>
							{/each}
						</ul>
					</div>
				{/if}
			</div>
		</div>
	{/if}

	<!-- Step 2 (manual): business asset type -->
	{#if currentStep === 2 && isManual && manual}
		<div
			class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-6"
		>
			<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
				<h3 class="text-base font-semibold text-gray-900 dark:text-gray-100 flex items-center">
					<IconifyIcon
						icon="material-symbols:category"
						class="h-5 w-5 mr-2 text-earthy-terracotta-600"
					/>
					{m.assetnew_manual_type_heading()}
				</h3>
				{#if manual.values.length >= ALPHABETICAL_FROM}
					<input
						type="search"
						bind:value={typeQuery}
						placeholder={m.common_search()}
						aria-label={m.common_search()}
						class="w-full sm:w-64 rounded-lg border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-earthy-terracotta-600"
					/>
				{/if}
			</div>
			<div role="radiogroup" aria-label={m.assetnew_manual_type_heading()} class="space-y-4">
				{#each typeGroups.groups as entry (entry.group ?? '')}
					<div role="group" aria-label={entry.group ? groupLabel(entry.group) : undefined}>
						{#if entry.group}
							<p
								class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400"
							>
								{groupLabel(entry.group)}
							</p>
						{/if}
						<div class="flex flex-wrap gap-2">
							{#each entry.values as value (value)}
								<label
									class="cursor-pointer rounded-full border px-3 py-1.5 text-sm transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-earthy-terracotta-600 {manualType ===
									value
										? 'border-earthy-terracotta-600 bg-earthy-terracotta-50 font-medium text-earthy-terracotta-700 dark:bg-earthy-terracotta-900/20 dark:text-earthy-terracotta-200'
										: 'border-gray-300 text-gray-700 hover:border-gray-400 dark:border-gray-600 dark:text-gray-200'}"
								>
									<input
										type="radio"
										name="manual-type"
										class="sr-only"
										{value}
										checked={manualType === value}
										onchange={() => (manualType = value)}
									/>
									{manualLabel(value)}
								</label>
							{/each}
						</div>
					</div>
				{:else}
					<p class="text-sm text-gray-500 dark:text-gray-400">{m.common_no_results()}</p>
				{/each}
			</div>
			{#if manualHelp}
				<p
					aria-live="polite"
					class="mt-4 rounded-lg bg-gray-50 dark:bg-gray-900/40 px-3 py-2 text-sm text-gray-600 dark:text-gray-300"
				>
					<span class="font-medium text-gray-900 dark:text-gray-100"
						>{manualLabel(manualType)}.</span
					>
					{manualHelp}
				</p>
			{/if}
		</div>
	{/if}

	<!-- Step 2: Type & Providers -->
	{#if currentStep === 2 && !isManual}
		<div
			class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-6"
		>
			<h3 class="text-base font-semibold text-gray-900 dark:text-gray-100 mb-4 flex items-center">
				<IconifyIcon
					icon="material-symbols:category"
					class="h-5 w-5 mr-2 text-earthy-terracotta-600"
				/>
				{m.assetnew_type_providers_heading()}
			</h3>

			<div class="space-y-6">
				<!-- Type -->
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
						{m.common_type()} <span class="text-red-500">*</span>
					</label>
					<div class="relative">
						<input
							type="text"
							bind:value={typeSearch}
							oninput={() => {
								assetType = typeSearch;
								showTypeDropdown = true;
								if (touched['type']) validateField('type');
								else clearFieldError('type');
							}}
							onfocus={() => (showTypeDropdown = true)}
							onblur={() => {
								setTimeout(() => (showTypeDropdown = false), 200);
								validateField('type');
							}}
							onkeydown={handleTypeKeydown}
							placeholder={m.assetnew_type_placeholder()}
							class="w-full px-4 py-2.5 border rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-earthy-terracotta-600 focus:border-transparent transition-all font-mono {fieldErrors[
								'type'
							] && touched['type']
								? 'border-red-500 dark:border-red-500'
								: 'border-gray-300 dark:border-gray-600'}"
						/>
						{#if showTypeDropdown && filteredTypes.length > 0}
							<div
								bind:this={typeDropdownElement}
								class="absolute z-10 w-full mt-1 bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg shadow-lg max-h-60 overflow-y-auto"
							>
								{#each filteredTypes as typeObj, index (typeObj.key)}
									<button
										type="button"
										onclick={() => {
											selectType(typeObj);
											clearFieldError('type');
										}}
										class="w-full px-4 py-3 flex items-center gap-3 hover:bg-gray-50 dark:hover:bg-gray-600 transition-colors text-left {index ===
										selectedTypeIndex
											? 'bg-earthy-terracotta-50 dark:bg-earthy-terracotta-900/30'
											: ''}"
									>
										<Icon name={typeObj.key} showLabel={false} size="md" />
										<span class="text-gray-900 dark:text-gray-100">{typeObj.display}</span>
									</button>
								{/each}
							</div>
						{/if}
					</div>
					{#if fieldErrors['type'] && touched['type']}
						<p class="mt-1.5 text-sm text-red-600 dark:text-red-400 flex items-center">
							<IconifyIcon icon="material-symbols:error" class="h-4 w-4 mr-1 flex-shrink-0" />
							{fieldErrors['type']}
						</p>
					{:else}
						<p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
							{m.assetnew_type_hint()}
						</p>
					{/if}
				</div>

				<!-- Providers -->
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
						{m.assetnew_providers_label()} <span class="text-red-500">*</span>
					</label>

					{#if providers.length > 0}
						<div class="flex flex-wrap gap-2 mb-2">
							{#each providers as provider, index (index)}
								<span
									class="inline-flex items-center gap-2 px-3 py-1.5 bg-earthy-terracotta-50 dark:bg-earthy-terracotta-900/30 text-earthy-terracotta-700 dark:text-earthy-terracotta-100 rounded-lg border border-earthy-terracotta-200 dark:border-earthy-terracotta-800"
								>
									<span class="text-sm font-medium">{provider}</span>
									<button
										type="button"
										onclick={() => {
											removeProvider(provider);
											validateField('providers');
										}}
										class="text-earthy-terracotta-700 dark:text-earthy-terracotta-700 hover:text-earthy-terracotta-700 dark:hover:text-earthy-terracotta-200 transition-colors"
									>
										<IconifyIcon icon="material-symbols:close" class="w-4 h-4" />
									</button>
								</span>
							{/each}
						</div>
					{/if}

					<div class="relative">
						<input
							type="text"
							bind:value={providerSearch}
							onfocus={() => (showProviderDropdown = true)}
							onblur={() => {
								setTimeout(() => (showProviderDropdown = false), 200);
								validateField('providers');
							}}
							onkeydown={handleProviderKeydown}
							placeholder={m.assetnew_providers_placeholder()}
							class="w-full px-4 py-2.5 border rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-earthy-terracotta-600 focus:border-transparent transition-all font-mono {fieldErrors[
								'providers'
							] &&
							touched['providers'] &&
							providers.length === 0
								? 'border-red-500 dark:border-red-500'
								: 'border-gray-300 dark:border-gray-600'}"
						/>
						{#if showProviderDropdown && filteredProviders.length > 0}
							<div
								bind:this={providerDropdownElement}
								class="absolute z-10 w-full mt-1 bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg shadow-lg max-h-60 overflow-y-auto"
							>
								{#each filteredProviders as providerObj, index (providerObj.key)}
									<button
										type="button"
										onclick={() => {
											toggleProvider(providerObj);
											clearFieldError('providers');
										}}
										class="w-full px-4 py-3 flex items-center gap-3 hover:bg-gray-50 dark:hover:bg-gray-600 transition-colors text-left {index ===
										selectedProviderIndex
											? 'bg-blue-50 dark:bg-blue-900/30'
											: providers.includes(providerObj.display)
												? 'bg-earthy-terracotta-50 dark:bg-earthy-terracotta-900/20'
												: ''}"
									>
										<Icon name={providerObj.key} showLabel={false} size="md" />
										<span class="text-gray-900 dark:text-gray-100 flex-1"
											>{providerObj.display}</span
										>
										{#if providers.includes(providerObj.display)}
											<IconifyIcon
												icon="material-symbols:check"
												class="w-5 h-5 text-earthy-terracotta-700 dark:text-earthy-terracotta-700"
											/>
										{/if}
									</button>
								{/each}
							</div>
						{/if}
					</div>
					{#if fieldErrors['providers'] && touched['providers']}
						<p class="mt-1.5 text-sm text-red-600 dark:text-red-400 flex items-center">
							<IconifyIcon icon="material-symbols:error" class="h-4 w-4 mr-1 flex-shrink-0" />
							{fieldErrors['providers']}
						</p>
					{:else}
						<p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
							{m.assetnew_providers_hint()}
						</p>
					{/if}
				</div>
			</div>
		</div>
	{/if}

	<!-- Step 3: Details -->
	{#if currentStep === 3}
		<div
			class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 p-6"
		>
			<h3 class="text-base font-semibold text-gray-900 dark:text-gray-100 mb-4 flex items-center">
				<IconifyIcon
					icon="material-symbols:description"
					class="h-5 w-5 mr-2 text-earthy-terracotta-600"
				/>
				{m.assetnew_details_heading()}
				<span class="ml-2 text-xs font-normal text-gray-500">{m.assetnew_optional_suffix()}</span>
			</h3>

			<div class="space-y-6">
				<!-- Description -->
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
						{m.common_description()}
					</label>
					<textarea
						bind:value={userDescription}
						placeholder={m.assetnew_description_placeholder()}
						rows="4"
						class="w-full px-4 py-2.5 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-earthy-terracotta-600 focus:border-transparent transition-all resize-none"
					></textarea>
					<p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
						{m.assetnew_description_hint()}
					</p>
				</div>

				<!-- Tags -->
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">
						{m.common_tags()}
					</label>
					<TagsInput bind:tags placeholder={m.assetnew_tags_placeholder()} />
					<p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
						{m.assetnew_tags_hint()}
					</p>
				</div>

				<DomainSelect id="asset-domain" bind:value={domainId} />
			</div>
		</div>

		<!-- Summary Card -->
		<div
			class="mt-6 bg-gray-50 dark:bg-gray-800/50 rounded-xl border border-gray-200 dark:border-gray-700 p-6"
		>
			<h4 class="text-sm font-semibold text-gray-900 dark:text-gray-100 mb-4 flex items-center">
				<IconifyIcon icon="material-symbols:summarize" class="h-5 w-5 mr-2 text-gray-500" />
				{m.assetnew_summary_heading()}
			</h4>
			<dl class="grid grid-cols-1 sm:grid-cols-2 gap-4 text-sm">
				<div>
					<dt class="text-gray-500 dark:text-gray-400">{m.common_name()}</dt>
					<dd class="font-medium text-gray-900 dark:text-gray-100 font-mono">{name}</dd>
				</div>
				<div>
					<dt class="text-gray-500 dark:text-gray-400">{m.common_type()}</dt>
					<dd class="font-medium text-gray-900 dark:text-gray-100">
						{isManual ? manualLabel(manualType) : assetType}
					</dd>
				</div>
				<div class="sm:col-span-2" hidden={isManual}>
					<dt class="text-gray-500 dark:text-gray-400">{m.assetnew_providers_label()}</dt>
					<dd class="flex flex-wrap gap-1.5 mt-1">
						{#each providers as provider (provider)}
							<span
								class="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-medium bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300"
							>
								{provider}
							</span>
						{/each}
					</dd>
				</div>
				{#if userDescription}
					<div class="sm:col-span-2">
						<dt class="text-gray-500 dark:text-gray-400">{m.common_description()}</dt>
						<dd class="font-medium text-gray-900 dark:text-gray-100">{userDescription}</dd>
					</div>
				{/if}
				{#if tags.length > 0}
					<div class="sm:col-span-2">
						<dt class="text-gray-500 dark:text-gray-400">{m.common_tags()}</dt>
						<dd class="flex flex-wrap gap-1.5 mt-1">
							{#each tags as tag (tag)}
								<span
									class="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-medium bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300"
								>
									{tag}
								</span>
							{/each}
						</dd>
					</div>
				{/if}
			</dl>
		</div>
	{/if}
</StepperPage>
