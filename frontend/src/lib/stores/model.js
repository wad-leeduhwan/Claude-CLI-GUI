import { writable } from 'svelte/store';
import { GetCurrentModel } from '../../../wailsjs/go/main/App';

function createModelStore() {
  const { subscribe, set } = writable('');
  return {
    subscribe,
    set,
    async refresh() {
      try {
        set(await GetCurrentModel());
      } catch (e) {
        console.warn('[ModelStore]', e);
      }
    }
  };
}

export const modelStore = createModelStore();
