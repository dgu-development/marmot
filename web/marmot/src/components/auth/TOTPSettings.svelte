<script lang="ts">
	import { onMount } from 'svelte';
	import { fetchApi } from '$lib/api';
	import { auth } from '$lib/stores/auth';
	import { m } from '$lib/paraglide/messages';
	import Button from '$components/ui/Button.svelte';

	let status = $state<{ local: boolean; enabled: boolean; recovery_remaining: number } | null>(
		null
	);
	let setup = $state<{ secret: string; uri: string; qr: string } | null>(null);
	let password = $state('');
	let code = $state('');
	let recovery = $state<string[]>([]);
	let busy = $state(false);
	let error = $state('');
	let action = $state<'setup' | 'disable' | 'recovery' | null>(null);

	onMount(async () => {
		try {
			const config = await fetch('/auth-providers');
			if (!config.ok || !(await config.json()).totp_enabled) return;
			const response = await fetchApi('/users/totp');
			if (!response.ok) throw new Error(m.totp_error());
			status = await response.json();
		} catch {
			error = m.totp_error();
		}
	});

	async function submit() {
		if (!action || busy) return;
		busy = true;
		error = '';
		const path = setup
			? '/users/totp/confirm'
			: action === 'disable'
				? '/users/totp'
				: `/users/totp/${action}`;
		try {
			const response = await fetch(`/api/v1${path}`, {
				method: action === 'disable' ? 'DELETE' : 'POST',
				headers: {
					'Content-Type': 'application/json',
					'X-Marmot-Client': 'web',
					Authorization: `Bearer ${auth.getToken() || ''}`
				},
				body: JSON.stringify({ password, code: code.trim() })
			});
			if (!response.ok)
				throw new Error(
					response.status === 503
						? m.totp_unavailable()
						: response.status === 429
							? m.totp_rate_limited()
							: m.totp_invalid_code()
				);
			const result = await response.json();
			if (result.secret) {
				setup = result;
			} else {
				if (result.access_token) auth.setToken(result.access_token);
				recovery = result.recovery_codes || [];
				if (status)
					status = {
						...status,
						enabled: action !== 'disable',
						recovery_remaining: recovery.length
					};
				setup = null;
				action = null;
			}
		} catch (err) {
			error = err instanceof Error ? err.message : m.totp_error();
		} finally {
			busy = false;
			password = '';
			code = '';
		}
	}

	function cancel() {
		action = null;
		setup = null;
		password = '';
		code = '';
		error = '';
	}
</script>

{#if error}<p role="alert" class="mt-5 text-sm text-red-700 dark:text-red-300">{error}</p>{/if}
{#if status?.local}
	<section
		class="mt-8 rounded-lg border border-gray-200 bg-white p-6 dark:border-gray-700 dark:bg-gray-800"
		aria-labelledby="totp-title"
	>
		<h2 id="totp-title" class="text-lg font-semibold">{m.totp_title()}</h2>
		<p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{m.totp_local_help()}</p>
		{#if recovery.length}
			<div class="mt-5 space-y-4">
				<p class="text-sm font-medium">{m.totp_recovery_help()}</p>
				<ul
					class="grid grid-cols-1 gap-2 rounded-md bg-gray-50 p-4 font-mono text-sm dark:bg-gray-900 sm:grid-cols-2"
				>
					{#each recovery as item (item)}<li>{item}</li>{/each}
				</ul>
				<Button text={m.totp_saved_codes()} variant="filled" click={() => (recovery = [])} />
			</div>
		{:else if action}
			<form
				class="mt-5 space-y-4"
				onsubmit={(event) => {
					event.preventDefault();
					void submit();
				}}
			>
				{#if setup}
					<p class="text-sm">{m.totp_scan_help()}</p>
					{#if setup.qr}<img
							src={setup.qr}
							alt={m.totp_qr_alt()}
							width="240"
							height="240"
							class="rounded border bg-white p-2"
						/>{/if}
					<p class="break-all font-mono text-sm select-all">{setup.secret}</p>
				{:else}
					<label class="block text-sm font-medium" for="totp-password"
						>{m.login_password_label()}</label
					>
					<input
						id="totp-password"
						type="password"
						autocomplete="current-password"
						bind:value={password}
						required
						maxlength="72"
						class="block w-full max-w-md rounded-md border border-gray-300 bg-white p-2.5 dark:border-gray-600 dark:bg-gray-700"
					/>
				{/if}
				{#if setup || action !== 'setup'}
					<label class="block text-sm font-medium" for="totp-settings-code">{m.totp_code()}</label>
					<input
						id="totp-settings-code"
						autocomplete="one-time-code"
						bind:value={code}
						required
						maxlength="128"
						class="block w-full max-w-md rounded-md border border-gray-300 bg-white p-2.5 font-mono dark:border-gray-600 dark:bg-gray-700"
					/>
				{/if}
				<div class="flex gap-3">
					<Button
						type="submit"
						loading={busy}
						text={setup
							? m.totp_verify()
							: action === 'disable'
								? m.totp_disable()
								: action === 'recovery'
									? m.totp_regenerate()
									: m.totp_setup()}
						variant="filled"
					/>
					<Button text={m.common_cancel()} click={cancel} disabled={busy} variant="clear" />
				</div>
			</form>
		{:else if status.enabled}
			<p class="mt-4 text-sm">{m.totp_enabled({ count: String(status.recovery_remaining) })}</p>
			<div class="mt-4 flex flex-wrap gap-3">
				<Button text={m.totp_regenerate()} click={() => (action = 'recovery')} variant="clear" />
				<Button text={m.totp_disable()} click={() => (action = 'disable')} variant="clear" />
			</div>
		{:else}
			<div class="mt-4">
				<Button text={m.totp_setup()} click={() => (action = 'setup')} variant="filled" />
			</div>
		{/if}
	</section>
{/if}
