import type { MetamodelField } from './types';

export function isMetadataStorage(storage: string): boolean {
	return storage.startsWith('metadata.');
}

export function metadataPath(storage: string): string[] {
	return storage.slice('metadata.'.length).split('.').filter(Boolean);
}

export function metadataTopLevelKey(storage: string): string | null {
	if (!isMetadataStorage(storage)) return null;
	return metadataPath(storage)[0] ?? null;
}

export function governedMetadataNamespaces(fields: MetamodelField[]): string[] {
	const keys = new Set<string>();
	for (const field of fields) {
		const key = metadataTopLevelKey(field.storage);
		if (key) keys.add(key);
	}
	return [...keys];
}

export function readMetadataValue(
	metadata: Record<string, unknown> | undefined,
	storage: string
): unknown {
	if (!metadata || !isMetadataStorage(storage)) return undefined;
	let value: unknown = metadata;
	for (const part of metadataPath(storage)) {
		if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
		value = (value as Record<string, unknown>)[part];
	}
	return value;
}

export function configurableFields(fields: MetamodelField[]): MetamodelField[] {
	return fields
		.filter((field) => isMetadataStorage(field.storage))
		.sort(
			(a, b) =>
				(a.presentation?.order ?? 0) - (b.presentation?.order ?? 0) || a.id.localeCompare(b.id)
		);
}

export function parseIfMatchETag(header: string | null): number | null {
	if (!header) return null;
	const trimmed = header.trim();
	if (trimmed.length < 3 || trimmed[0] !== '"' || trimmed.at(-1) !== '"') return null;
	const version = Number(trimmed.slice(1, -1));
	return Number.isInteger(version) && version > 0 ? version : null;
}

export function assetETag(version: number): string {
	return `"${version}"`;
}
