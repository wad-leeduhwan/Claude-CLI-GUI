import { writable } from 'svelte/store';

const STORAGE_KEY = 'claude-gui-font-size';
const DEFAULT_SIZE = 100;
const MIN_SIZE = 70;
const MAX_SIZE = 150;
const STEP = 10;

function createFontSizeStore() {
  const saved = typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) : null;
  const initial = saved ? Math.max(MIN_SIZE, Math.min(MAX_SIZE, Number(saved))) : DEFAULT_SIZE;
  const { subscribe, set, update } = writable(initial);

  return {
    subscribe,
    increase() {
      update(current => {
        const next = Math.min(MAX_SIZE, current + STEP);
        localStorage.setItem(STORAGE_KEY, String(next));
        return next;
      });
    },
    decrease() {
      update(current => {
        const next = Math.max(MIN_SIZE, current - STEP);
        localStorage.setItem(STORAGE_KEY, String(next));
        return next;
      });
    },
    reset() {
      localStorage.setItem(STORAGE_KEY, String(DEFAULT_SIZE));
      set(DEFAULT_SIZE);
    }
  };
}

export const fontSize = createFontSizeStore();
