import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { fieldsForAssetType } from '../src/lib/metamodel/values.ts';
import type { MetamodelField } from '../src/lib/metamodel/types.ts';

const field = (id: string, assetTypes?: string[]) =>
	({
		id,
		type: 'string',
		storage: `metadata.dgu.${id}`,
		appliesTo: { assetTypes }
	}) as unknown as MetamodelField;

const fields = [field('asset_type'), field('retention'), field('threshold', ['technical_rule'])];
const ids = (metadata: Record<string, unknown>) =>
	fieldsForAssetType(fields, metadata).map((f) => f.id);

test('a scoped field only shows on its own asset type', () => {
	assert.deepEqual(ids({ dgu: { asset_type: 'technical_rule' } }), [
		'asset_type',
		'retention',
		'threshold'
	]);
	assert.deepEqual(ids({ dgu: { asset_type: 'table' } }), ['asset_type', 'retention']);
});

test('an untyped asset shows no scoped field', () => {
	assert.deepEqual(ids({}), ['asset_type', 'retention']);
});
