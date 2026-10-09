import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
	panelFields,
	panelTabs,
	panelsFor,
	type EntityPanel
} from '../src/lib/extensions/entity-panels.ts';

const load = () => Promise.reject(new Error('not loaded in tests'));
const panels: EntityPanel[] = [
	{ id: 'graph', placement: 'tab', load },
	{
		id: 'org',
		placement: 'tab',
		assetTypes: ['line_of_business'],
		fields: ['parent_area', 'related_areas'],
		load
	}
];

test('a panel scoped to asset types shows only on assets of those types', () => {
	const ids = (kind: 'asset' | 'data_product', type?: string) =>
		panelTabs(panels, kind, type).map((tab) => tab.id);
	assert.deepEqual(ids('asset', 'line_of_business'), ['ext-graph', 'ext-org']);
	assert.deepEqual(ids('asset', 'table'), ['ext-graph']);
	assert.deepEqual(ids('asset'), ['ext-graph']);
	assert.deepEqual(ids('data_product', 'line_of_business'), ['ext-graph']);
	assert.equal(panelsFor(panels, 'asset', 'side', 'line_of_business').length, 0);
});

test('the fields a panel edits are taken from the sheet only where the panel shows', () => {
	assert.deepEqual(panelFields(panels, 'asset', 'line_of_business'), [
		'parent_area',
		'related_areas'
	]);
	assert.deepEqual(panelFields(panels, 'asset', 'table'), []);
});
