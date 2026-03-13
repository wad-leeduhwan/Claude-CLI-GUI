import { writable } from 'svelte/store';

// Tracks which tab currently has focus for keyboard shortcuts
export const focusedTabId = writable(null);
