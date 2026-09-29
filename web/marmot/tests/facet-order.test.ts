import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { orderFacetFields } from '../src/lib/metamodel/values.ts';
import type { MetamodelField } from '../src/lib/metamodel/types.ts';

const field = (id: string, facet = true) =>
	({
		id,
		type: 'enum',
		storage: `metadata.dgu.${id}`,
		presentation: { facet }
	}) as unknown as MetamodelField;

test('family and asset type lead the facets and demote the native type', () => {
	const order = orderFacetFields([
		field('classification'),
		field('asset_type'),
		field('asset_family'),
		field('notes', false)
	]);
	assert.deepEqual(
		order.lead.map((f) => f.id),
		['asset_family', 'asset_type']
	);
	assert.deepEqual(
		order.rest.map((f) => f.id),
		['classification']
	);
	assert.equal(order.technicalType, true);
});

test('without an asset type the native type keeps its place', () => {
	const order = orderFacetFields([field('classification')]);
	assert.deepEqual(order.lead, []);
	assert.equal(order.technicalType, false);
});
