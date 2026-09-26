import type { Component } from 'svelte';
import { writable } from 'svelte/store';

export type EntityKind = 'asset' | 'data_product' | 'glossary_term';

/** The entity a panel describes; `mrn` is set for assets. */
export interface EntityRef {
	kind: EntityKind;
	id: string;
	mrn?: string;
}

/**
 * A card a distribution adds to the side column of asset, data product and
 * glossary term pages. `load` is only called on a page that shows the panel,
 * so the panel's code stays out of the main bundle.
 */
export interface EntityPanel {
	id: string;
	kinds?: EntityKind[];
	load: () => Promise<{ default: Component<{ entity: EntityRef }> }>;
}

export const entityPanels = writable<EntityPanel[]>([]);

/** Adds a panel, replacing one with the same id; returns its removal. */
export function registerEntityPanel(panel: EntityPanel): () => void {
	entityPanels.update((panels) => [...panels.filter((item) => item.id !== panel.id), panel]);
	return () => entityPanels.update((panels) => panels.filter((item) => item !== panel));
}
