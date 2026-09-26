import type { Component } from 'svelte';
import { writable } from 'svelte/store';

export type EntityKind = 'asset' | 'data_product' | 'glossary_term';

/** The entity a panel describes; `mrn` is set for assets. */
export interface EntityRef {
	kind: EntityKind;
	id: string;
	mrn?: string;
}

/** Where a panel renders: a card in the side column, or a tab of the page. */
export type PanelPlacement = 'side' | 'tab';

/**
 * A view a distribution adds to asset, data product and glossary term pages.
 * `load` is only called on a page that shows the panel, so its code stays out of
 * the main bundle. A `tab` panel falls back to the side column on pages without tabs.
 */
export interface EntityPanel {
	id: string;
	kinds?: EntityKind[];
	placement?: PanelPlacement;
	/** Label and icon of the tab; `label` is read on render so it follows the locale. */
	tab?: { label: () => string; icon: string };
	load: () => Promise<{
		default: Component<{ entity: EntityRef; placement: PanelPlacement }>;
	}>;
}

export const entityPanels = writable<EntityPanel[]>([]);

/** Adds a panel, replacing one with the same id; returns its removal. */
export function registerEntityPanel(panel: EntityPanel): () => void {
	entityPanels.update((panels) => [...panels.filter((item) => item.id !== panel.id), panel]);
	return () => entityPanels.update((panels) => panels.filter((item) => item !== panel));
}

export function panelsFor(panels: EntityPanel[], kind: EntityKind, placement: PanelPlacement) {
	return panels.filter(
		(panel) =>
			(!panel.kinds || panel.kinds.includes(kind)) && (panel.placement ?? 'side') === placement
	);
}

/** Tab entries for a page's tab bar; their ids start with `ext-`. */
export function panelTabs(panels: EntityPanel[], kind: EntityKind) {
	return panelsFor(panels, kind, 'tab').map((panel) => ({
		id: `ext-${panel.id}`,
		label: panel.tab?.label() ?? panel.id,
		icon: panel.tab?.icon ?? 'material-symbols:extension'
	}));
}
