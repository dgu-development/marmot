import { fetchApi } from '$lib/api';

/** A term at the other end of a profile field with the glossary_term control. */
export interface TermRef {
	id: string;
	name: string;
	definition: string;
}

/** The live terms whose `field` points at a term. */
export interface TermReferences {
	field: string;
	terms: TermRef[];
}

export const GLOSSARY_TERM_CONTROL = 'glossary_term';

const known = new Map<string, TermRef | null>();

/** The term IDs a glossary_term value holds, as a string or a list. */
export function linkIds(value: unknown): string[] {
	if (typeof value === 'string') return value ? [value] : [];
	if (Array.isArray(value))
		return value.filter((v): v is string => typeof v === 'string' && v !== '');
	return [];
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
						`/glossary/refs?ids=${encodeURIComponent(batch.join(','))}`
					);
					if (!response.ok) throw new Error('Failed to resolve glossary terms');
					const found = (await response.json()) as TermRef[];
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

/** Resolves IDs to terms, caching them; a deleted or unknown ID maps to null. */
export async function resolveTerms(ids: string[]): Promise<Map<string, TermRef | null>> {
	const missing = ids.filter((id) => !known.has(id));
	if (missing.length > 0) {
		for (const id of missing) queued.add(id);
		await fetchQueued();
	}
	return new Map(ids.map((id) => [id, known.get(id) ?? null]));
}

export function rememberTerm(ref: TermRef) {
	known.set(ref.id, ref);
}

export async function termReferences(id: string): Promise<TermReferences[]> {
	const response = await fetchApi(`/glossary/references/${encodeURIComponent(id)}`);
	if (!response.ok) throw new Error('Failed to load term references');
	return response.json();
}

export async function findTerms(query: string, limit = 8): Promise<TermRef[]> {
	const params = new URLSearchParams({ q: query, limit: String(limit) });
	const response = await fetchApi(`/glossary/search?${params}`);
	if (!response.ok) throw new Error('Failed to search glossary terms');
	const body = (await response.json()) as { terms?: TermRef[] };
	return (body.terms ?? []).map(({ id, name, definition }) => ({ id, name, definition }));
}
