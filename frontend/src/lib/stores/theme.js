import { writable } from 'svelte/store';

const STORAGE_KEY = 'claude-gui-theme';

function createThemeStore() {
  // Load from localStorage, default to 'light'
  const saved = typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) : null;
  const initial = saved === 'dark' ? 'dark' : 'light';

  const { subscribe, set, update } = writable(initial);

  return {
    subscribe,
    toggle() {
      update(current => {
        const next = current === 'dark' ? 'light' : 'dark';
        localStorage.setItem(STORAGE_KEY, next);
        return next;
      });
    },
    set(value) {
      localStorage.setItem(STORAGE_KEY, value);
      set(value);
    }
  };
}

export const theme = createThemeStore();
