import assert from 'node:assert/strict';

function isMetadataStorage(storage) {
	return storage.startsWith('metadata.');
}
function metadataPath(storage) {
	return storage.slice('metadata.'.length).split('.').filter(Boolean);
}
function readMetadataValue(metadata, storage) {
	if (!metadata || !isMetadataStorage(storage)) return undefined;
	let value = metadata;
	for (const part of metadataPath(storage)) {
		if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
		value = value[part];
	}
	return value;
}
function parseIfMatchETag(header) {
	if (!header) return null;
	const trimmed = header.trim();
	if (trimmed.length < 3 || trimmed[0] !== '"' || trimmed.at(-1) !== '"') return null;
	const version = Number(trimmed.slice(1, -1));
	return Number.isInteger(version) && version > 0 ? version : null;
}

assert.equal(readMetadataValue({ example: { retention: 30 } }, 'metadata.example.retention'), 30);
assert.equal(readMetadataValue({ example: {} }, 'metadata.example.retention'), undefined);
assert.equal(parseIfMatchETag('"3"'), 3);
assert.equal(parseIfMatchETag('3'), null);
assert.equal(parseIfMatchETag('W/"3"'), null);
console.log('metamodel values self-check ok');
