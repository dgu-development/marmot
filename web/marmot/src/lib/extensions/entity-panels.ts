import type { Component } from 'svelte';
import { writable } from 'svelte/store';

export type EntityKind = 'asset' | 'data_product' | 'glossary_term' | 'domain';

/** The entity a panel describes; `mrn` is set for assets. */
export interface EntityRef {
	kind: EntityKind;
	id: string;
	mrn?: string;
	/** The profile's asset type of an asset, when it has one. */
	assetType?: string;
}

/**
 * Where a panel renders: a card in the side column, a tab of the page, the right end of the
 * page header, or the heading of the asset references card.
 */
export type PanelPlacement = 'side' | 'tab' | 'header' | 'references';

/**
 * A view a distribution adds to asset, data product and glossary term pages. A domain page
 * shows only the `header` ones, next to its own actions.
 * `load` is only called on a page that shows the panel, so its code stays out of
 * the main bundle. A `tab` panel falls back to the side column on pages without tabs.
 */
export interface EntityPanel {
	id: string;
	kinds?: EntityKind[];
	/** Profile asset types the panel is for; empty shows it on every asset. */
	assetTypes?: string[];
	/** Profile fields the panel edits itself: the metadata sheet leaves them out where it shows. */
	fields?: string[];
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

function applies(panel: EntityPanel, kind: EntityKind, assetType?: string): boolean {
	if (panel.kinds && !panel.kinds.includes(kind)) return false;
	if (!panel.assetTypes?.length) return true;
	return kind === 'asset' && !!assetType && panel.assetTypes.includes(assetType);
}

export function panelsFor(
	panels: EntityPanel[],
	kind: EntityKind,
	placement: PanelPlacement,
	assetType?: string
) {
	return panels.filter(
		(panel) => applies(panel, kind, assetType) && (panel.placement ?? 'side') === placement
	);
}

/** Ids of the profile fields that the panels shown for this entity edit themselves. */
export function panelFields(panels: EntityPanel[], kind: EntityKind, assetType?: string): string[] {
	return panels
		.filter((panel) => applies(panel, kind, assetType))
		.flatMap((panel) => panel.fields ?? []);
}

/** Tab entries for a page's tab bar; their ids start with `ext-`. */
export function panelTabs(panels: EntityPanel[], kind: EntityKind, assetType?: string) {
	return panelsFor(panels, kind, 'tab', assetType).map((panel) => ({
		id: `ext-${panel.id}`,
		label: panel.tab?.label() ?? panel.id,
		icon: panel.tab?.icon ?? 'material-symbols:extension'
	}));
}
