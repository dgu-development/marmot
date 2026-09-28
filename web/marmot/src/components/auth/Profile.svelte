<script lang="ts">
	import { onMount } from 'svelte';
	import { fetchApi } from '$lib/api';
	import { m } from '$lib/paraglide/messages';
	import { formatDate } from '$lib/utils';
	import ThemeToggle from '$components/ui/ThemeToggle.svelte';
	import LanguageSelector from '$components/ui/LanguageSelector.svelte';
	import NotificationPreferencesToggle from '$components/ui/NotificationPreferencesToggle.svelte';

	interface Permission {
		name: string;
		description: string;
	}

	interface Role {
		name: string;
		permissions: Permission[];
	}

	interface User {
		username: string;
		active: boolean;
		created_at: string;
		updated_at: string;
		name: string;
		email: string;
		roles: Role[];
	}

	let loading = true;
	let error: string | null = null;
	let user: User = {
		username: '',
		active: false,
		created_at: '',
		updated_at: '',
		name: '',
		email: '',
		roles: []
	};
	$: permissions = [
		...new Map(user.roles.flatMap((role) => role.permissions).map((p) => [p.name, p])).values()
	];

	function permissionLabel(permission: Permission): string {
		const labels: Record<string, () => string> = {
			view_users: m.profile_permission_view_users,
			manage_users: m.profile_permission_manage_users,
			view_assets: m.profile_permission_view_assets,
			manage_assets: m.profile_permission_manage_assets,
			manage_roles: m.profile_permission_manage_roles,
			view_metrics: m.profile_permission_view_metrics,
			view_glossary: m.profile_permission_view_glossary,
			manage_glossary: m.profile_permission_manage_glossary,
			view_teams: m.profile_permission_view_teams,
			manage_teams: m.profile_permission_manage_teams,
			manage_sso_mappings: m.profile_permission_manage_sso_mappings,
			view_ingestion: m.profile_permission_view_ingestion,
			manage_ingestion: m.profile_permission_manage_ingestion,
			preview_assets: m.profile_permission_preview_assets,
			emit_agent_runs: m.profile_permission_emit_agent_runs,
			service_accounts_view: m.profile_permission_service_accounts_view,
			service_accounts_manage: m.profile_permission_service_accounts_manage,
			dgu_view_domains: m.profile_permission_dgu_view_domains,
			dgu_manage_domains: m.profile_permission_dgu_manage_domains
		};
		return labels[permission.name]?.() || permission.description || permission.name;
	}
	onMount(fetchProfile);

	async function fetchProfile() {
		try {
			loading = true;
			error = null;
			const response = await fetchApi('/users/me');
			if (!response.ok) {
				throw new Error(m.profile_load_error());
			}
			user = await response.json();
		} catch (err) {
			console.error('Profile fetch error:', err);
			error = err instanceof Error ? err.message : m.profile_load_error();
		} finally {
			loading = false;
		}
	}
</script>

<div
	class="bg-earthy-brown-50 dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 divide-y divide-gray-200 dark:divide-gray-700"
>
	<!-- Basic Information -->
	<div class="p-6">
		<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100">
			{m.profile_information_heading()}
		</h3>
		{#if loading}
			<div class="mt-4">{m.common_loading()}</div>
		{:else if error}
			<div class="mt-4 text-red-600">{error}</div>
		{:else}
			<dl class="mt-4 grid grid-cols-1 gap-x-4 gap-y-6 sm:grid-cols-2">
				<div>
					<dt class="text-sm font-medium text-gray-500 dark:text-gray-500">{m.common_name()}</dt>
					<dd class="mt-1 text-sm text-gray-900 dark:text-gray-100">{user.name}</dd>
				</div>
				<div>
					<dt class="text-sm font-medium text-gray-500 dark:text-gray-500">
						{m.profile_username_label()}
					</dt>
					<dd class="mt-1 text-sm text-gray-900 dark:text-gray-100">{user.username}</dd>
				</div>
				<div>
					<dt class="text-sm font-medium text-gray-500 dark:text-gray-500">
						{m.profile_account_created_label()}
					</dt>
					<dd class="mt-1 text-sm text-gray-900 dark:text-gray-100">
						{formatDate(user.created_at)}
					</dd>
				</div>
				<div>
					<dt class="text-sm font-medium text-gray-500 dark:text-gray-500">
						{m.profile_last_updated_label()}
					</dt>
					<dd class="mt-1 text-sm text-gray-900 dark:text-gray-100">
						{formatDate(user.updated_at)}
					</dd>
				</div>
			</dl>
		{/if}
	</div>

	<!-- User Preferences -->
	<div class="p-6">
		<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-4">
			{m.profile_preferences_heading()}
		</h3>
		<div class="space-y-4">
			<div>
				<h4 class="text-sm font-medium text-gray-500 dark:text-gray-500 mb-2">
					{m.profile_theme_label()}
				</h4>
				<ThemeToggle />
			</div>
			<div>
				<h4 class="text-sm font-medium text-gray-500 dark:text-gray-500 mb-2">
					{m.profile_language_label()}
				</h4>
				<LanguageSelector />
			</div>
			<div>
				<h4 class="text-sm font-medium text-gray-500 dark:text-gray-500 mb-2">
					{m.profile_notifications_label()}
				</h4>
				<NotificationPreferencesToggle />
			</div>
		</div>
	</div>

	<!-- Roles and Permissions -->
	<div class="p-6">
		<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-4">
			{m.profile_roles_permissions_heading()}
		</h3>
		<div class="space-y-6">
			<!-- Roles -->
			<div>
				<h4 class="text-sm font-medium text-gray-500 dark:text-gray-500 mb-2">
					{m.profile_assigned_roles_heading()}
				</h4>
				<div class="flex flex-wrap gap-2">
					{#each user.roles as role (role.name)}
						<span
							class="inline-flex items-center px-3 py-1 rounded-full text-sm font-medium bg-earthy-terracotta-100 dark:bg-earthy-terracotta-900 text-earthy-terracotta-700 dark:text-earthy-terracotta-100"
						>
							{role.name}
						</span>
					{/each}
				</div>
			</div>

			<div class="overflow-hidden rounded-lg border border-gray-200 dark:border-gray-700">
				<div
					class="flex items-center justify-between gap-3 border-b border-gray-200 bg-earthy-brown-100 px-4 py-3 dark:border-gray-700 dark:bg-gray-800"
				>
					<h4 class="text-sm font-semibold text-gray-900 dark:text-gray-100">
						{m.profile_permissions_heading()}
					</h4>
					<span
						class="rounded-full bg-white px-2.5 py-0.5 text-xs font-semibold tabular-nums text-gray-600 dark:bg-gray-700 dark:text-gray-200"
						aria-label={m.profile_permissions_count({ count: String(permissions.length) })}
						>{permissions.length}</span
					>
				</div>
				<ul
					class="max-h-80 divide-y divide-gray-200 overflow-y-auto dark:divide-gray-700"
					aria-label={m.profile_permissions_heading()}
				>
					{#each permissions as permission (permission.name)}
						<li
							class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 py-2.5 text-sm"
						>
							<span class="font-medium text-gray-900 dark:text-gray-100"
								>{permissionLabel(permission)}</span
							>
							<code class="text-xs text-gray-500 dark:text-gray-400">{permission.name}</code>
						</li>
					{/each}
				</ul>
			</div>
		</div>
	</div>

	<!-- Account Status -->
	<div class="p-6">
		<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-4">
			{m.profile_account_status_heading()}
		</h3>
		<div class="flex items-center space-x-2">
			<span
				class={`inline-flex items-center px-3 py-1 rounded-full text-sm font-medium ${user.active ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'}`}
			>
				{user.active ? m.common_active() : m.common_inactive()}
			</span>
		</div>
	</div>
</div>
