<script lang="ts">
	import Profile from '$components/auth/Profile.svelte';
	import ApiKeys from '$components/auth/ApiKeys.svelte';
	import Subscriptions from '$components/auth/Subscriptions.svelte';
	import Sidebar from '$components/ui/Sidebar.svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { auth } from '$lib/stores/auth';
	import { m } from '$lib/paraglide/messages';

	// The API key endpoints require users:manage, so without it the tab could only fail.
	const canManageApiKeys = auth.hasPermission('users', 'manage');
	const tabs = [
		{ id: 'profile', label: m.profile_tab_profile() },
		{ id: 'subscriptions', label: m.profile_tab_subscriptions() },
		...(canManageApiKeys ? [{ id: 'api-keys', label: m.profile_tab_api_keys() }] : [])
	];

	$: activeTab = $page.url.searchParams.get('tab') || tabs[0]?.id;

	onMount(() => {
		if (!$page.url.searchParams.has('tab')) {
			goto(resolve(`/profile?tab=${tabs[0]?.id}`), { replaceState: true });
		}
		if ($page.url.searchParams.get('tab') === 'api-keys' && !canManageApiKeys) {
			goto(resolve('/profile?tab=profile'), { replaceState: true });
		}
	});
</script>

<div class="container max-w-7xl mx-auto py-6 px-4 sm:px-6 lg:px-8">
	<div class="flex flex-col lg:flex-row gap-6">
		<Sidebar {tabs} />

		<div class="flex-1">
			{#if activeTab === 'profile'}
				<Profile />
			{:else if activeTab === 'subscriptions'}
				<Subscriptions />
			{:else if activeTab === 'api-keys'}
				<ApiKeys />
			{/if}
		</div>
	</div>
</div>
