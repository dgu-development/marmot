import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { groupedValues } from '../src/lib/metamodel/values.ts';
import type { MetamodelField } from '../src/lib/metamodel/types.ts';

const type = { id: 'asset_type', type: 'enum' } as unknown as MetamodelField;
const family = {
	id: 'asset_family',
	type: 'enum',
	values: ['business', 'ai', 'data'],
	derive: { from: 'asset_type', map: { kpi: 'business', policy: 'governance', ai_agent: 'ai' } }
} as unknown as MetamodelField;

test('values are grouped by the field derived from them, in its order, and the rest come last', () => {
	const { by, groups } = groupedValues(
		['policy', 'ai_agent', 'kpi', 'loose'],
		[type, family],
		'asset_type'
	);
	assert.equal(by?.id, 'asset_family');
	assert.deepEqual(groups, [
		{ group: 'business', values: ['kpi'] },
		{ group: 'ai', values: ['ai_agent'] },
		{ group: null, values: ['policy', 'loose'] }
	]);
});

test('without a derived field there is one group', () => {
	assert.deepEqual(groupedValues(['kpi'], [type], 'asset_type'), {
		groups: [{ group: null, values: ['kpi'] }]
	});
});
