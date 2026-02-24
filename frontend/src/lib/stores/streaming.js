import { writable, get } from 'svelte/store';
import { orchestratorStore } from './orchestrator.js';

// Global streaming state per tab
function createStreamingStore() {
  const { subscribe, update, set } = writable({});
  let ws = null;
  let reconnectTimeout = null;

  // Connect to WebSocket server
  function connect(port = 9876) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      console.log('[StreamingStore] WebSocket already connected');
      return;
    }

    const url = `ws://127.0.0.1:${port}/ws`;
    console.log(`[StreamingStore] Connecting to WebSocket: ${url}`);

    try {
      ws = new WebSocket(url);

      ws.onopen = () => {
        console.log('[StreamingStore] WebSocket connected!');
        if (reconnectTimeout) {
          clearTimeout(reconnectTimeout);
          reconnectTimeout = null;
        }
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          console.log(`[StreamingStore] WebSocket message:`, msg.type, msg.tabId || msg.adminTabId, msg.content?.length || 0);

          // Route orchestrator events to orchestratorStore
          if (msg.adminTabId && ['task-started', 'task-completed', 'task-failed', 'job-completed'].includes(msg.type)) {
            orchestratorStore.handleEvent(msg);
            return;
          }

          switch (msg.type) {
            case 'start':
              update(state => ({
                ...state,
                [msg.tabId]: { isStreaming: true, content: '' }
              }));
              break;

            case 'chunk':
              update(state => ({
                ...state,
                [msg.tabId]: { isStreaming: true, content: msg.content }
              }));
              break;

            case 'end':
              update(state => ({
                ...state,
                [msg.tabId]: {
                  isStreaming: false,
                  content: state[msg.tabId]?.content || ''
                }
              }));
              break;

            case 'error':
              console.error('[StreamingStore] Stream error:', msg.error);
              update(state => ({
                ...state,
                [msg.tabId]: { isStreaming: false, content: '', error: msg.error }
              }));
              break;
          }
        } catch (e) {
          console.error('[StreamingStore] Failed to parse message:', e);
        }
      };

      ws.onclose = () => {
        console.log('[StreamingStore] WebSocket disconnected, reconnecting in 2s...');
        ws = null;
        reconnectTimeout = setTimeout(() => connect(port), 2000);
      };

      ws.onerror = (error) => {
        console.error('[StreamingStore] WebSocket error:', error);
      };

    } catch (e) {
      console.error('[StreamingStore] Failed to connect:', e);
      reconnectTimeout = setTimeout(() => connect(port), 2000);
    }
  }

  // Disconnect WebSocket
  function disconnect() {
    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout);
      reconnectTimeout = null;
    }
    if (ws) {
      ws.close();
      ws = null;
    }
  }

  return {
    subscribe,
    connect,
    disconnect,
    // Fallback methods for Wails events (still used as backup)
    start: (tabId) => {
      update(state => ({
        ...state,
        [tabId]: { isStreaming: true, content: '' }
      }));
    },
    addChunk: (tabId, content) => {
      update(state => ({
        ...state,
        [tabId]: { isStreaming: true, content }
      }));
    },
    end: (tabId) => {
      update(state => ({
        ...state,
        [tabId]: {
          isStreaming: false,
          content: state[tabId]?.content || ''
        }
      }));
    },
    clear: (tabId) => {
      update(state => {
        const newState = { ...state };
        delete newState[tabId];
        return newState;
      });
    }
  };
}

export const streamingStore = createStreamingStore();
