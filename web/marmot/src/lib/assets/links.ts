import { fetchApi } from '$lib/api';

/** An asset at the other end of a profile field with the asset control. */
export interface AssetRef {
	id: string;
	name: string;
	type: string;
	providers: string[];
	mrn?: string;
}

/** The assets whose `field` points at an asset. */
export interface AssetReferences {
	field: string;
	assets: AssetRef[];
}

export const ASSET_CONTROL = 'asset';

const known = new Map<string, AssetRef | null>();

/** The asset IDs an asset-control value holds, as a string or a list. */
export function assetLinkIds(value: unknown): string[] {
	if (typeof value === 'string') return value ? [value] : [];
	if (Array.isArray(value))
		return value.filter((v): v is string => typeof v === 'string' && v !== '');
	return [];
}

/** The URL segments of an asset's page: /discover/{type}/{provider}/{name}. */
export function assetPath(ref: AssetRef): { type: string; provider: string; name: string } {
	return {
		type: encodeURIComponent(ref.type.toLowerCase()),
		provider: encodeURIComponent((ref.providers[0] ?? '').toLowerCase()),
		name: encodeURIComponent(ref.name)
	};
}

let queued = new Set<string>();
let flush: Promise<void> | null = null;

// Callers in the same tick, such as every row of a list, share one request.
function fetchQueued(): Promise<void> {
	flush ??= new Promise<void>((done, fail) => {
		setTimeout(async () => {
			const ids = [...queued];
			queued = new Set();
			flush = null;
			try {
				for (let i = 0; i < ids.length; i += 100) {
					const batch = ids.slice(i, i + 100);
					const response = await fetchApi(
						`/assets/refs?ids=${encodeURIComponent(batch.join(','))}`
					);
					if (!response.ok) throw new Error('Failed to resolve assets');
					const found = (await response.json()) as AssetRef[];
					for (const id of batch) known.set(id, null);
					for (const ref of found) known.set(ref.id, ref);
				}
				done();
			} catch (err) {
				fail(err);
			}
		});
	});
	return flush;
}

/** Resolves IDs to assets, caching them; a deleted or unknown ID maps to null. */
export async function resolveAssets(ids: string[]): Promise<Map<string, AssetRef | null>> {
	const missing = ids.filter((id) => !known.has(id));
	if (missing.length > 0) {
		for (const id of missing) queued.add(id);
		await fetchQueued();
	}
	return new Map(ids.map((id) => [id, known.get(id) ?? null]));
}

export function rememberAsset(ref: AssetRef) {
	known.set(ref.id, ref);
}

export async function assetReferences(id: string): Promise<AssetReferences[]> {
	const response = await fetchApi(`/assets/references/${encodeURIComponent(id)}`);
	if (!response.ok) throw new Error('Failed to load asset references');
	return response.json();
}

export async function findAssets(query: string, limit = 8): Promise<AssetRef[]> {
	const params = new URLSearchParams({ q: query, limit: String(limit) });
	const response = await fetchApi(`/assets/search?${params}`);
	if (!response.ok) throw new Error('Failed to search assets');
	const body = (await response.json()) as { assets?: AssetRef[] };
	return (body.assets ?? []).map(({ id, name, type, providers, mrn }) => ({
		id,
		name,
		type,
		providers: providers ?? [],
		mrn
	}));
}
