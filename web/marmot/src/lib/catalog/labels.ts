import { derived, writable } from 'svelte/store';
import { locale } from '$lib/i18n';
import { m } from '$lib/paraglide/messages';
import { fetchMetamodel } from '$lib/metamodel/api';

/** Lowercase, and anything but a-z0-9 to "_": `Delta Table` → `delta_table`. */
export function catalogKey(id: string): string {
	return id.toLowerCase().replace(/[^a-z0-9]/g, '_');
}

// Static references keep check-i18n and tree-shaking working; the types are the ones the core
// plugins emit.
const nativeTypes: Record<string, () => string> = {
	alias: m.asset_type_alias,
	bucket: m.asset_type_bucket,
	catalog: m.asset_type_catalog,
	channel: m.asset_type_channel,
	chart: m.asset_type_chart,
	collection: m.asset_type_collection,
	container: m.asset_type_container,
	crawler: m.asset_type_crawler,
	dashboard: m.asset_type_dashboard,
	data_model_object: m.asset_type_data_model_object,
	data_stream: m.asset_type_data_stream,
	database: m.asset_type_database,
	dataset: m.asset_type_dataset,
	datasource: m.asset_type_datasource,
	endpoint: m.asset_type_endpoint,
	exchange: m.asset_type_exchange,
	function: m.asset_type_function,
	instance: m.asset_type_instance,
	job: m.asset_type_job,
	keyspace: m.asset_type_keyspace,
	model: m.asset_type_model,
	namespace: m.asset_type_namespace,
	pipeline: m.asset_type_pipeline,
	queue: m.asset_type_queue,
	service: m.asset_type_service,
	stream: m.asset_type_stream,
	subject: m.asset_type_subject,
	subscription: m.asset_type_subscription,
	table: m.asset_type_table,
	task: m.asset_type_task,
	topic: m.asset_type_topic,
	view: m.asset_type_view
};

interface ProfileMessages {
	messages?: Record<string, Record<string, string>>;
	defaultLocale: string;
}

export interface CatalogLabels {
	/** An asset type's label: the profile's `assetType.<key>`, Marmot's, or the type itself. */
	type: (id: string | undefined | null) => string;
	/** A provider's label: the profile's `provider.<key>`, or the provider itself. */
	provider: (id: string | undefined | null) => string;
}

const profile = writable<ProfileMessages>({ defaultLocale: 'en' });
let requested = false;

function load() {
	if (requested) return;
	requested = true;
	fetchMetamodel()
		.then((schema) =>
			profile.set({ messages: schema.messages, defaultLocale: schema.defaultLocale })
		)
		.catch(() => {
			requested = false;
		});
}

/** Labels for the catalog's own identifiers; what is stored and filtered stays the identifier. */
export const catalogLabels = derived<[typeof profile, typeof locale], CatalogLabels>(
	[profile, locale],
	([$profile, $locale], set) => {
		load();
		const fromProfile = (key: string) =>
			$profile.messages?.[$locale]?.[key] ?? $profile.messages?.[$profile.defaultLocale]?.[key];
		set({
			type: (id) => {
				if (!id) return '';
				const key = catalogKey(id);
				return fromProfile(`assetType.${key}`) ?? nativeTypes[key]?.() ?? id;
			},
			provider: (id) => (id ? (fromProfile(`provider.${catalogKey(id)}`) ?? id) : '')
		});
	}
);
