<script>
  import { onMount, onDestroy } from 'svelte';
  import SplitPane from './SplitPane.svelte';
  import { GetTabs, AddNewTab, RemoveTab, GetWebSocketPort, GetClaudeVersion } from '../../../wailsjs/go/main/App';
  import { EventsOn, EventsOff } from '../../../wailsjs/runtime/runtime';
  import { streamingStore } from '../stores/streaming.js';
  import { modelStore } from '../stores/model.js';
  import { theme } from '../stores/theme.js';

  $: isDark = $theme === 'dark';

  function toggleTheme() {
    theme.toggle();
  }

  let tabs = [];
  let rootNode = null;
  let nodeIdCounter = 0;
  let isAnyTabStreaming = false;
  let pendingTabsUpdate = false;
  let claudeVersion = '';

  // Event handler functions (defined outside onMount for cleanup)
  function handleUserMessageAdded(tabID) {
    console.log(`[EVENT ${Date.now()}] user-message-added:`, tabID, 'isStreaming:', isAnyTabStreaming);
    loadTabs();
  }

  function handleTabsUpdated() {
    console.log(`[EVENT ${Date.now()}] tabs-updated, isStreaming:`, isAnyTabStreaming, 'pending:', pendingTabsUpdate);
    modelStore.refresh();
    if (isAnyTabStreaming) {
      console.log('[DynamicSplitLayout] Streaming in progress, deferring tabs update');
      pendingTabsUpdate = true;
    } else {
      loadTabs();
    }
  }

  function handleStreamingStart(tabId) {
    console.log(`[EVENT ${Date.now()}] streaming-start:`, tabId);
    isAnyTabStreaming = true;
    streamingStore.start(tabId);
  }

  function handleStreamingChunk(data) {
    console.log(`[EVENT ${Date.now()}] streaming-chunk:`, data.tabId, 'length:', data.content?.length);
    streamingStore.addChunk(data.tabId, data.content);
  }

  function handleStreamingEnd(tabId) {
    console.log(`[EVENT ${Date.now()}] streaming-end:`, tabId);
    isAnyTabStreaming = false;
    streamingStore.end(tabId);
    // Process pending tabs update after streaming ends
    if (pendingTabsUpdate) {
      console.log('[DynamicSplitLayout] Processing deferred tabs update');
      pendingTabsUpdate = false;
      setTimeout(() => loadTabs(), 100);
    }
  }

  onMount(async () => {
    console.log('[DynamicSplitLayout] Component mounted');

    // Connect to WebSocket for real-time streaming
    try {
      const port = await GetWebSocketPort();
      console.log(`[DynamicSplitLayout] Connecting to WebSocket on port ${port}`);
      streamingStore.connect(port);
    } catch (e) {
      console.error('[DynamicSplitLayout] Failed to get WebSocket port:', e);
      // Fallback to default port
      streamingStore.connect(9876);
    }

    // Clean up any existing listeners first (prevent duplicates)
    EventsOff('user-message-added');
    EventsOff('tabs-updated');
    EventsOff('streaming-start');
    EventsOff('streaming-chunk');
    EventsOff('streaming-end');

    // Register Wails event listeners (backup for non-streaming events)
    EventsOn('user-message-added', handleUserMessageAdded);
    EventsOn('tabs-updated', handleTabsUpdated);
    // Keep streaming events as backup in case WebSocket fails
    EventsOn('streaming-start', handleStreamingStart);
    EventsOn('streaming-chunk', handleStreamingChunk);
    EventsOn('streaming-end', handleStreamingEnd);

    console.log('[DynamicSplitLayout] Event listeners registered');
    await loadTabs();

    // Initialize model store
    await modelStore.refresh();

    // Load Claude CLI version
    try {
      claudeVersion = await GetClaudeVersion();
    } catch (e) {
      claudeVersion = 'unknown';
    }
  });

  onDestroy(() => {
    console.log('[DynamicSplitLayout] Component destroyed, cleaning up');
    // Disconnect WebSocket
    streamingStore.disconnect();
    // Clean up Wails event listeners
    EventsOff('user-message-added');
    EventsOff('tabs-updated');
    EventsOff('streaming-start');
    EventsOff('streaming-chunk');
    EventsOff('streaming-end');
  });

  async function loadTabs() {
    try {
      console.log('[DynamicSplitLayout] Loading tabs...');
      const newTabs = await GetTabs();
      console.log('[DynamicSplitLayout] Tabs loaded:', newTabs.length, 'tabs');
      newTabs.forEach(tab => {
        console.log(`  Tab ${tab.id}: ${tab.messages?.length || 0} messages`);
      });

      // Force reactivity by creating a new array reference
      tabs = [...newTabs];

      // Initialize layout with all tabs in horizontal split
      if (!rootNode && tabs.length > 0) {
        rootNode = createInitialLayout(tabs);
      }
    } catch (error) {
      console.error('Failed to load tabs:', error);
    }
  }

  function createInitialLayout(tabList) {
    return createGridLayout(tabList);
  }

  function createGridLayout(tabList) {
    if (tabList.length === 0) return null;

    if (tabList.length === 1) {
      return {
        id: nodeIdCounter++,
        type: 'leaf',
        tabId: tabList[0].id,
        size: 1
      };
    }

    // Create grid with max 3 columns
    const rows = [];
    let currentIndex = 0;

    while (currentIndex < tabList.length) {
      const rowTabs = tabList.slice(currentIndex, currentIndex + 3);

      if (rowTabs.length === 1) {
        rows.push({
          id: nodeIdCounter++,
          type: 'leaf',
          tabId: rowTabs[0].id,
          size: 1.0
        });
      } else {
        rows.push({
          id: nodeIdCounter++,
          type: 'container',
          direction: 'horizontal',
          size: 1.0,
          children: rowTabs.map(tab => ({
            id: nodeIdCounter++,
            type: 'leaf',
            tabId: tab.id,
            size: 1.0
          }))
        });
      }

      currentIndex += 3;
    }

    if (rows.length === 1) {
      return rows[0];
    }

    return {
      id: nodeIdCounter++,
      type: 'container',
      direction: 'vertical',
      size: 1.0,
      children: rows.map(row => ({...row, size: 1.0}))
    };
  }

  async function handleAddNewTab() {
    if (tabs.length >= 6) {
      alert('최대 6개의 탭만 생성할 수 있습니다.');
      return;
    }

    try {
      const newTab = await AddNewTab();
      tabs = [...tabs, newTab];

      // Add new leaf to the layout
      if (!rootNode) {
        rootNode = {
          id: nodeIdCounter++,
          type: 'leaf',
          tabId: newTab.id,
          size: 1.0
        };
      } else {
        // Add to the end of the layout
        addTabToLayout(rootNode, newTab.id);
      }
    } catch (error) {
      console.error('Failed to add new tab:', error);
      alert('새 탭 추가 실패');
    }
  }

  function handleOrganize() {
    if (!tabs || tabs.length === 0) return;

    // Reorganize all tabs into a clean grid layout
    rootNode = createGridLayout(tabs);
  }

  function addTabToLayout(node, tabId) {
    // Find the last container and add to it
    if (node.type === 'container') {
      if (node.children.length < 3) {
        node.children.push({
          id: nodeIdCounter++,
          type: 'leaf',
          tabId: tabId,
          size: 1.0
        });
        // Normalize sizes to ensure equal distribution
        node.children = node.children.map(child => ({...child, size: 1.0}));
      } else {
        addTabToLayout(node.children[node.children.length - 1], tabId);
      }
    } else {
      // Convert leaf to container
      const oldTabId = node.tabId;
      node.type = 'container';
      node.direction = 'horizontal';
      node.children = [
        { id: nodeIdCounter++, type: 'leaf', tabId: oldTabId, size: 1.0 },
        { id: nodeIdCounter++, type: 'leaf', tabId: tabId, size: 1.0 }
      ];
      delete node.tabId;
    }

    rootNode = {...rootNode}; // Force reactivity
  }

  function handleSplit(event) {
    const { targetNode, draggedTabId, direction } = event.detail;

    // Ignore drop on self
    if (targetNode.tabId === draggedTabId) {
      return;
    }

    if (direction === 'center') {
      // Swap tabs (exchange positions)
      swapTabs(rootNode, targetNode.tabId, draggedTabId);
    } else {
      // Move dragged tab to split with target
      moveAndSplitTab(targetNode, draggedTabId, direction);
    }

    rootNode = {...rootNode}; // Force reactivity
  }

  function swapTabs(node, targetTabId, draggedTabId) {
    if (node.type === 'leaf') {
      if (node.tabId === targetTabId) {
        node.tabId = draggedTabId;
      } else if (node.tabId === draggedTabId) {
        node.tabId = targetTabId;
      }
    } else if (node.type === 'container') {
      node.children.forEach(child => swapTabs(child, targetTabId, draggedTabId));
    }
  }

  function moveAndSplitTab(targetNode, draggedTabId, direction) {
    // Step 1: Remove dragged tab from its current position
    const draggedNodeRemoved = removeTabNodeFromTree(rootNode, draggedTabId);

    // Step 2: Split the target node to include both target and dragged tab
    splitNodeWithTab(targetNode, draggedTabId, direction);

    // Step 3: Clean up tree structure
    cleanupTree(rootNode);
  }

  function removeTabNodeFromTree(node, tabId) {
    if (!node) return false;

    if (node.type === 'leaf') {
      return node.tabId === tabId;
    } else if (node.type === 'container') {
      node.children = node.children.filter(child => {
        return !removeTabNodeFromTree(child, tabId);
      });

      // Flatten single-child containers
      if (node.children.length === 1 && node.children[0].type === 'container') {
        const child = node.children[0];
        node.direction = child.direction;
        node.children = child.children;
      }

      return node.children.length === 0;
    }

    return false;
  }

  function cleanupTree(node) {
    if (!node || node.type === 'leaf') return;

    if (node.type === 'container') {
      // Recursively clean children
      node.children.forEach(child => cleanupTree(child));

      // Remove empty containers
      node.children = node.children.filter(child => {
        if (child.type === 'container') {
          return child.children.length > 0;
        }
        return true;
      });

      // Flatten single-child containers
      if (node.children.length === 1 && node.children[0].type === 'container') {
        const child = node.children[0];
        node.direction = child.direction;
        node.children = child.children;
      }
    }
  }

  function splitNodeWithTab(targetNode, draggedTabId, direction) {
    // Find parent of target node
    const parent = findParent(rootNode, targetNode.id);

    const splitDirection = (direction === 'top' || direction === 'bottom') ? 'vertical' : 'horizontal';
    const first = direction === 'top' || direction === 'left';

    const targetTabId = targetNode.tabId;

    if (!parent) {
      // Target is root
      rootNode = {
        id: nodeIdCounter++,
        type: 'container',
        direction: splitDirection,
        size: 1.0,
        children: first ? [
          { id: nodeIdCounter++, type: 'leaf', tabId: draggedTabId, size: 1.0 },
          { id: nodeIdCounter++, type: 'leaf', tabId: targetTabId, size: 1.0 }
        ] : [
          { id: nodeIdCounter++, type: 'leaf', tabId: targetTabId, size: 1.0 },
          { id: nodeIdCounter++, type: 'leaf', tabId: draggedTabId, size: 1.0 }
        ]
      };
    } else {
      // Insert split in parent
      const targetIndex = parent.children.findIndex(c => c.id === targetNode.id);

      const newContainer = {
        id: nodeIdCounter++,
        type: 'container',
        direction: splitDirection,
        size: 1.0,
        children: first ? [
          { id: nodeIdCounter++, type: 'leaf', tabId: draggedTabId, size: 1.0 },
          { id: nodeIdCounter++, type: 'leaf', tabId: targetTabId, size: 1.0 }
        ] : [
          { id: nodeIdCounter++, type: 'leaf', tabId: targetTabId, size: 1.0 },
          { id: nodeIdCounter++, type: 'leaf', tabId: draggedTabId, size: 1.0 }
        ]
      };

      parent.children[targetIndex] = newContainer;
    }
  }

  function findParent(node, childId, parent = null) {
    if (node.type === 'container') {
      for (const child of node.children) {
        if (child.id === childId) {
          return node;
        }
        const found = findParent(child, childId, node);
        if (found) return found;
      }
    }
    return null;
  }

  function handleUpdate() {
    console.log('[DynamicSplitLayout] handleUpdate called');
    loadTabs();
  }

  async function handleClose(event) {
    const { tabId } = event.detail;

    // Remove tab from layout tree
    if (rootNode) {
      const wasRemoved = removeTabNodeFromTree(rootNode, tabId);

      if (wasRemoved) {
        // Root itself was removed
        rootNode = null;
      } else {
        cleanupTree(rootNode);
        rootNode = {...rootNode}; // Force reactivity
      }

      // If root becomes empty container, set to null
      if (rootNode && rootNode.type === 'container' && rootNode.children.length === 0) {
        rootNode = null;
      }
    }

    // Reload tabs from backend to update count
    await loadTabs();
  }

  $: canAddTab = tabs.length < 6;
</script>

<div class="layout-container">
  <div class="toolbar">
    <button class="add-tab-btn" on:click={handleAddNewTab} disabled={!canAddTab}>
      <span class="icon">+</span>
      새 탭 {tabs.length}/6
    </button>
    <button class="organize-btn" on:click={handleOrganize}>
      정렬
    </button>
    <div class="toolbar-spacer"></div>
    {#if claudeVersion}
      <span class="cli-version">{claudeVersion}</span>
    {/if}
    <label class="theme-toggle">
      <input type="checkbox" checked={isDark} on:change={toggleTheme} />
      <span>{isDark ? '다크 모드' : '라이트 모드'}</span>
    </label>
  </div>

  <div class="workspace">
    {#if rootNode}
      <SplitPane node={rootNode} {tabs} on:split={handleSplit} on:update={handleUpdate} on:close={handleClose} />
    {:else}
      <div class="empty-state">
        <p>+ 새 탭 버튼을 눌러 대화를 시작하세요</p>
      </div>
    {/if}
  </div>
</div>

<style>
  .layout-container {
    display: flex;
    flex-direction: column;
    height: 100vh;
    background-color: var(--bg-primary);
    overflow: hidden;
  }

  .toolbar {
    display: flex;
    align-items: center;
    padding: 8px 12px;
    background-color: var(--bg-secondary);
    border-bottom: 1px solid var(--border-primary);
    gap: 12px;
    flex-shrink: 0;
  }

  .toolbar-spacer {
    flex: 1;
  }

  .cli-version {
    font-size: 11px;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    padding: 2px 8px;
    border: 1px solid var(--border-primary);
    border-radius: 3px;
    white-space: nowrap;
  }

  .theme-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    cursor: pointer;
    user-select: none;
  }

  .theme-toggle input[type="checkbox"] {
    width: 14px;
    height: 14px;
    cursor: pointer;
  }

  .add-tab-btn {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    background-color: var(--accent);
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 13px;
    transition: background-color 0.2s;
  }

  .add-tab-btn:hover:not(:disabled) {
    background-color: var(--accent-hover);
  }

  .add-tab-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .add-tab-btn .icon {
    font-size: 16px;
    font-weight: bold;
  }

  .organize-btn {
    padding: 6px 12px;
    background-color: #0e639c;
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 13px;
    transition: background-color 0.2s;
  }

  .organize-btn:hover {
    background-color: #1177bb;
  }

  .workspace {
    flex: 1;
    overflow: hidden;
    display: flex;
  }

  .empty-state {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
    font-size: 16px;
    gap: 1em;
  }

  .empty-state::before {
    content: '✨';
    font-size: 5em;
    opacity: 0.3;
  }

  .empty-state p {
    margin: 0;
    font-size: 1.2em;
  }
</style>
