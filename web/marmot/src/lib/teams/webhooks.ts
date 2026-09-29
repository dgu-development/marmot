import { m } from '$lib/paraglide/messages';

export type WebhookProvider = 'slack' | 'discord' | 'generic' | 'teams' | 'google_chat';

export interface TeamWebhook {
	id: string;
	team_id: string;
	name: string;
	provider: WebhookProvider;
	webhook_url: string;
	notification_types: string[];
	enabled: boolean;
	last_triggered_at?: string;
	last_error?: string;
	created_at: string;
	updated_at: string;
}

export interface CreateWebhookInput {
	name: string;
	provider: WebhookProvider;
	webhook_url: string;
	notification_types: string[];
	enabled?: boolean;
}

export interface UpdateWebhookInput {
	name?: string;
	webhook_url?: string;
	notification_types?: string[];
	enabled?: boolean;
}

// Built on call rather than at import so the labels follow a language change without a page reload
export const notificationTypeOptions = (): { type: string; label: string; icon: string }[] => [
	{ type: 'system', label: m.webhook_type_system(), icon: 'material-symbols:info-outline' },
	{
		type: 'schema_change',
		label: m.webhook_type_schema_change(),
		icon: 'material-symbols:schema-outline'
	},
	{
		type: 'asset_change',
		label: m.webhook_type_asset_change(),
		icon: 'material-symbols:database-outline'
	},
	{
		type: 'team_invite',
		label: m.webhook_type_team_invite(),
		icon: 'material-symbols:group-add-outline'
	},
	{ type: 'mention', label: m.webhook_type_mention(), icon: 'material-symbols:alternate-email' },
	{
		type: 'job_complete',
		label: m.webhook_type_job_complete(),
		icon: 'material-symbols:check-circle-outline'
	},
	{
		type: 'upstream_schema_change',
		label: m.webhook_type_upstream_schema_change(),
		icon: 'material-symbols:arrow-upward-alt'
	},
	{
		type: 'downstream_schema_change',
		label: m.webhook_type_downstream_schema_change(),
		icon: 'material-symbols:arrow-downward-alt'
	},
	{
		type: 'lineage_change',
		label: m.webhook_type_lineage_change(),
		icon: 'material-symbols:timeline'
	}
];

export const notificationTypeLabels = (): Record<string, string> =>
	Object.fromEntries(notificationTypeOptions().map((o) => [o.type, o.label]));

export const NOTIFICATION_TYPES = [
	'system',
	'schema_change',
	'asset_change',
	'team_invite',
	'mention',
	'job_complete',
	'upstream_schema_change',
	'downstream_schema_change',
	'lineage_change'
];

export const providerOptions = () => [
	{ value: 'slack', label: m.webhook_provider_slack(), icon: 'mdi:slack' },
	{ value: 'discord', label: m.webhook_provider_discord(), icon: 'mdi:discord' },
	{ value: 'teams', label: m.webhook_provider_teams(), icon: 'mdi:microsoft-teams' },
	{
		value: 'google_chat',
		label: m.webhook_provider_google_chat(),
		icon: 'simple-icons:googlechat'
	},
	{ value: 'generic', label: m.webhook_provider_generic(), icon: 'mdi:webhook' }
];

export const providerLabels = (): Record<string, string> => ({
	slack: m.webhook_provider_slack(),
	discord: m.webhook_provider_discord(),
	teams: m.webhook_provider_teams(),
	google_chat: m.webhook_provider_google_chat(),
	generic: m.webhook_provider_generic()
});
