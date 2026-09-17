import { derived, writable } from 'svelte/store';
import { browser } from '$app/environment';
import en from '../../../messages/en.json';
import { translate, type Messages, type Vars } from './catalog';

// Adding a catalogue is enough to make a language available; no component edits needed.
const files = import.meta.glob<Messages>('../../../messages/*.json', { eager: true, import: 'default' });
const catalogs = Object.fromEntries(Object.entries(files).map(([path, messages]) => [
	path.split('/').pop()!.replace(/\.json$/, ''), messages
]));
export const languages = Object.keys(catalogs).sort();
export const DEFAULT_LOCALE = 'en';
const STORAGE_KEY = 'marmot-language';
export function isLocale(value: unknown): value is string {
	return typeof value === 'string' && Object.hasOwn(catalogs, value);
}
function readLocale(): string {
	try {
		const value = browser ? localStorage.getItem(STORAGE_KEY) : null;
		return isLocale(value) ? value : DEFAULT_LOCALE;
	} catch { return DEFAULT_LOCALE; }
}
const store = writable(readLocale());
let revision = 0;
export const locale = {
	subscribe: store.subscribe,
	set(value: string) {
		if (!isLocale(value)) return;
		revision++;
		store.set(value);
		if (browser) {
			document.documentElement.lang = value;
			try { localStorage.setItem(STORAGE_KEY, value); } catch { /* Storage may be disabled. */ }
		}
	}
};
if (browser) document.documentElement.lang = readLocale();
export const t = derived(store, (language) => (key: string, vars?: Vars) =>
	translate(catalogs[language], en, key, vars)
);
export function localeRevision() { return revision; }
export type { Translate, Vars } from './catalog';
