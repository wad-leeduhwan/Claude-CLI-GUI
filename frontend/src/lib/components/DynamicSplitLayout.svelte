<script>
  import { onMount, onDestroy } from 'svelte';
  import SplitPane from './SplitPane.svelte';
  import { GetTabs, AddNewTab, RemoveTab, GetWebSocketPort, GetClaudeVersion, CheckForUpdate, GetAppVersion, DetectInstallMethod, UpdateClaude, GetReleaseNotes, TranslateReleaseNotes, OpenChangelogPage } from '../../../wailsjs/go/main/App';
  import { EventsOn, EventsOff } from '../../../wailsjs/runtime/runtime';
  import { streamingStore } from '../stores/streaming.js';
  import { modelStore } from '../stores/model.js';
  import { theme } from '../stores/theme.js';
  import { agentStore } from '../stores/agent.js';

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
  let appVersion = '';
  let updateInfo = null;
  let showUpdatePopup = false;
  let copySuccess = false;
  let updateCheckInterval = null;
  let isUpdating = false;
  let updateResult = null;
  let installMethod = null;
  let installMethodLoading = true;
  let releaseNotes = [];
  let releaseNotesLoading = false;
  let isTranslated = false;
  let isTranslating = false;
  let translatedNotes = [];

  const UPDATE_COMMAND_NPM = 'npm install -g @anthropic-ai/claude-code';
  const UPDATE_COMMAND_BREW = 'brew upgrade claude-code';

  async function checkUpdate() {
    try {
      const info = await CheckForUpdate();
      updateInfo = info;
      if (info.currentVersion) {
        claudeVersion = info.currentVersion;
      }
    } catch (e) {
      console.error('[DynamicSplitLayout] Failed to check for update:', e);
    }
  }

  function toggleUpdatePopup() {
    showUpdatePopup = !showUpdatePopup;
    if (showUpdatePopup && installMethod === null) {
      installMethodLoading = true;
      DetectInstallMethod().then(method => {
        installMethod = method;
        installMethodLoading = false;
      }).catch(() => {
        installMethod = 'unknown';
        installMethodLoading = false;
      });
    }
    if (showUpdatePopup && updateInfo?.updateAvailable && releaseNotes.length === 0) {
      releaseNotesLoading = true;
      GetReleaseNotes(updateInfo.currentVersion, updateInfo.latestVersion).then(notes => {
        releaseNotes = notes || [];
        releaseNotesLoading = false;
      }).catch(() => {
        releaseNotes = [];
        releaseNotesLoading = false;
      });
    }
  }

  function closeUpdatePopup() {
    showUpdatePopup = false;
  }

  async function handleAutoUpdate() {
    isUpdating = true;
    updateResult = null;
    try {
      const result = await UpdateClaude();
      updateResult = result;
      if (result.success && result.newVersion) {
        claudeVersion = result.newVersion;
        releaseNotes = [];
        await checkUpdate();
      }
    } catch (e) {
      updateResult = { success: false, error: e.toString(), output: '' };
    } finally {
      isUpdating = false;
    }
  }

  async function copyCommand(cmd) {
    try {
      await navigator.clipboard.writeText(cmd);
      copySuccess = true;
      setTimeout(() => { copySuccess = false; }, 2000);
    } catch (e) {
      console.error('Failed to copy:', e);
    }
  }

  async function translateNotes() {
    if (isTranslated) {
      isTranslated = false;
      return;
    }
    if (translatedNotes.length > 0) {
      isTranslated = true;
      return;
    }
    isTranslating = true;
    try {
      translatedNotes = await TranslateReleaseNotes();
      isTranslated = true;
    } catch (e) {
      console.error('[DynamicSplitLayout] Translation failed:', e);
    } finally {
      isTranslating = false;
    }
  }

  function openChangelog() {
    OpenChangelogPage();
  }

  function handleOutsideClick(event) {
    if (showUpdatePopup && !event.target.closest('.update-popup-container')) {
      closeUpdatePopup();
    }
  }

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

  // Agent event handlers
  function handleAgentTabRename(data) {
    console.log('[Agent] tab-rename:', data);
    agentStore.setTabRename(data.tabID, data.name);
  }

  function handleAgentProjectSummary(data) {
    console.log('[Agent] project-summary:', data);
    agentStore.setProjectSummary(data.workDir, data);
  }

  function handleAgentCodeReview(data) {
    console.log('[Agent] code-review:', data);
    agentStore.setCodeReview(data.tabID, data);
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
    EventsOff('agent-tab-rename');
    EventsOff('agent-project-summary');
    EventsOff('agent-code-review');

    // Register Wails event listeners (backup for non-streaming events)
    EventsOn('user-message-added', handleUserMessageAdded);
    EventsOn('tabs-updated', handleTabsUpdated);
    // Keep streaming events as backup in case WebSocket fails
    EventsOn('streaming-start', handleStreamingStart);
    EventsOn('streaming-chunk', handleStreamingChunk);
    EventsOn('streaming-end', handleStreamingEnd);

    // Agent background task events
    EventsOn('agent-tab-rename', handleAgentTabRename);
    EventsOn('agent-project-summary', handleAgentProjectSummary);
    EventsOn('agent-code-review', handleAgentCodeReview);

    console.log('[DynamicSplitLayout] Event listeners registered');
    await loadTabs();

    // Initialize model store
    await modelStore.refresh();

    // Load app version and Claude CLI version
    try {
      appVersion = await GetAppVersion();
    } catch (e) {
      appVersion = 'dev';
    }
    try {
      claudeVersion = await GetClaudeVersion();
    } catch (e) {
      claudeVersion = 'unknown';
    }

    // Check for updates
    await checkUpdate();

    // Re-check every 30 minutes
    updateCheckInterval = setInterval(checkUpdate, 30 * 60 * 1000);
  });

  onDestroy(() => {
    console.log('[DynamicSplitLayout] Component destroyed, cleaning up');
    // Clear update check interval
    if (updateCheckInterval) {
      clearInterval(updateCheckInterval);
      updateCheckInterval = null;
    }
    // Disconnect WebSocket
    streamingStore.disconnect();
    // Clean up Wails event listeners
    EventsOff('user-message-added');
    EventsOff('tabs-updated');
    EventsOff('streaming-start');
    EventsOff('streaming-chunk');
    EventsOff('streaming-end');
    EventsOff('agent-tab-rename');
    EventsOff('agent-project-summary');
    EventsOff('agent-code-review');
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
    {#if appVersion}
      <span class="app-version">{appVersion}</span>
    {/if}
    {#if claudeVersion}
      <div class="update-popup-container">
        {#if updateInfo?.updateAvailable}
          <button class="cli-version has-update" on:click={toggleUpdatePopup}>
            {claudeVersion} <span class="update-arrow">&#8593; {updateInfo.latestVersion}</span>
          </button>
        {:else}
          <span class="cli-version">{claudeVersion}</span>
        {/if}
        {#if showUpdatePopup}
          <!-- svelte-ignore a11y-click-events-have-key-events -->
          <div class="update-popup-backdrop" on:click={closeUpdatePopup}></div>
          <div class="update-guide-popup">
            <div class="update-popup-header">업데이트 안내</div>
            <div class="update-popup-versions">
              <span>현재: <strong>{updateInfo.currentVersion}</strong></span>
              <span>최신: <strong>{updateInfo.latestVersion}</strong></span>
            </div>

            <!-- Release notes section -->
            <div class="release-notes-section">
              <div class="release-notes-toolbar">
                <span class="release-notes-header">변경사항</span>
                <div class="release-notes-actions">
                  <button class="rn-action-btn" on:click={translateNotes} disabled={isTranslating}>
                    {isTranslating ? '번역 중...' : isTranslated ? '원문 보기' : '한글 번역'}
                  </button>
                  <button class="rn-action-btn" on:click={openChangelog}>
                    릴리즈 노트 →
                  </button>
                </div>
              </div>
              {#if releaseNotesLoading}
                <div class="release-notes-loading">
                  <span class="spinner"></span>
                  <span>릴리즈 노트 로딩 중...</span>
                </div>
              {:else if releaseNotes.length > 0}
                <div class="release-notes-list">
                  {#each (isTranslated ? translatedNotes : releaseNotes) as note}
                    <div class="release-note-item">
                      <div class="release-note-meta">
                        <span class="release-note-version">v{note.version}</span>
                        {#if note.publishedAt}
                          <span class="release-note-date">{new Date(note.publishedAt).toLocaleDateString()}</span>
                        {/if}
                      </div>
                      {#if note.body}
                        <ul class="release-note-body">
                          {#each note.body.split('\n').filter(l => l.trim()) as line}
                            <li>{line.replace(/^[-•]\s*/, '')}</li>
                          {/each}
                        </ul>
                      {:else}
                        <div class="release-note-body-empty">상세 내용 없음</div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {:else}
                <div class="release-notes-empty">릴리즈 노트를 불러올 수 없습니다.</div>
              {/if}
            </div>

            <!-- Auto update section -->
            <div class="auto-update-section">
              <div class="install-method-info">
                {#if installMethodLoading}
                  <span class="detect-loading">설치 방법 감지 중...</span>
                {:else if installMethod === 'brew'}
                  <span>설치 방법: <strong>Homebrew</strong></span>
                {:else if installMethod === 'npm'}
                  <span>설치 방법: <strong>npm</strong></span>
                {:else}
                  <span>설치 방법: <strong>알 수 없음</strong></span>
                {/if}
              </div>

              {#if !isUpdating && !updateResult}
                <button
                  class="auto-update-btn"
                  on:click={handleAutoUpdate}
                  disabled={installMethodLoading || installMethod === 'unknown'}
                >
                  자동 업데이트
                </button>
              {/if}

              {#if isUpdating}
                <div class="update-progress">
                  <span class="spinner"></span>
                  <span>업데이트 중...</span>
                </div>
              {/if}

              {#if updateResult}
                {#if updateResult.success}
                  <div class="update-success">
                    업데이트 완료! 새 버전: <strong>{updateResult.newVersion}</strong>
                  </div>
                {:else}
                  <div class="update-error">
                    <div>업데이트 실패: {updateResult.error}</div>
                    {#if updateResult.output}
                      <details class="update-log">
                        <summary>상세 로그</summary>
                        <pre>{updateResult.output}</pre>
                      </details>
                    {/if}
                    <button
                      class="auto-update-btn retry"
                      on:click={() => { updateResult = null; }}
                    >
                      다시 시도
                    </button>
                  </div>
                {/if}
              {/if}
            </div>

            <div class="update-popup-desc">또는 터미널에서 수동으로 실행:</div>
            <div class="update-command-label">npm</div>
            <div class="update-command">
              <code>{UPDATE_COMMAND_NPM}</code>
              <button class="copy-btn" on:click={() => copyCommand(UPDATE_COMMAND_NPM)} title="복사">
                {copySuccess ? '✓' : '📋'}
              </button>
            </div>
            <div class="update-command-label">Homebrew</div>
            <div class="update-command">
              <code>{UPDATE_COMMAND_BREW}</code>
              <button class="copy-btn" on:click={() => copyCommand(UPDATE_COMMAND_BREW)} title="복사">
                {copySuccess ? '✓' : '📋'}
              </button>
            </div>
            <div class="update-popup-footer">
              <button class="close-btn" on:click={closeUpdatePopup}>닫기</button>
            </div>
          </div>
        {/if}
      </div>
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

  .app-version {
    font-size: 11px;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    padding: 2px 8px;
    border: 1px solid var(--border-primary);
    border-radius: 3px;
    white-space: nowrap;
    margin-right: 6px;
  }

  .cli-version {
    font-size: 11px;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    padding: 2px 8px;
    border: 1px solid var(--border-primary);
    border-radius: 3px;
    white-space: nowrap;
    background: none;
  }

  .cli-version.has-update {
    cursor: pointer;
    border-color: #10b981;
    transition: background-color 0.2s, border-color 0.2s;
  }

  .cli-version.has-update:hover {
    background-color: rgba(16, 185, 129, 0.1);
  }

  .update-arrow {
    color: #10b981;
    font-weight: 600;
    margin-left: 4px;
  }

  .update-popup-container {
    position: relative;
  }

  .update-popup-backdrop {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 199;
  }

  .update-guide-popup {
    position: absolute;
    top: calc(100% + 8px);
    right: 0;
    z-index: 200;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 8px;
    padding: 16px;
    min-width: 340px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.3);
  }

  .update-popup-header {
    font-size: 14px;
    font-weight: 600;
    color: var(--text-primary);
    margin-bottom: 12px;
  }

  .update-popup-versions {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 12px;
    color: var(--text-secondary);
    margin-bottom: 12px;
  }

  .update-popup-versions strong {
    color: var(--text-primary);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .update-popup-desc {
    font-size: 12px;
    color: var(--text-secondary);
    margin-bottom: 8px;
  }

  .update-command-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 4px;
    margin-top: 8px;
  }

  .update-command {
    display: flex;
    align-items: center;
    gap: 8px;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    padding: 8px 10px;
  }

  .update-command code {
    flex: 1;
    font-size: 11px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    color: var(--text-primary);
    word-break: break-all;
  }

  .copy-btn {
    background: none;
    border: none;
    cursor: pointer;
    font-size: 14px;
    padding: 2px 4px;
    border-radius: 3px;
    transition: background-color 0.2s;
  }

  .copy-btn:hover {
    background-color: var(--border-primary);
  }

  .update-popup-footer {
    display: flex;
    justify-content: flex-end;
    margin-top: 12px;
  }

  .close-btn {
    font-size: 12px;
    padding: 4px 12px;
    background-color: var(--bg-primary);
    color: var(--text-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    cursor: pointer;
    transition: background-color 0.2s;
  }

  .close-btn:hover {
    background-color: var(--border-primary);
    color: var(--text-primary);
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

  /* Release notes styles */
  .release-notes-section {
    margin-bottom: 12px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border-primary);
  }

  .release-notes-toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }

  .release-notes-header {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .release-notes-actions {
    display: flex;
    gap: 4px;
    margin-left: auto;
  }

  .rn-action-btn {
    font-size: 10px;
    padding: 2px 6px;
    background: none;
    color: var(--text-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 3px;
    cursor: pointer;
    white-space: nowrap;
    transition: background-color 0.2s, color 0.2s;
  }

  .rn-action-btn:hover:not(:disabled) {
    background-color: var(--border-primary);
    color: var(--text-primary);
  }

  .rn-action-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .release-notes-loading {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-muted);
    padding: 8px 0;
  }

  .release-notes-list {
    max-height: 200px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .release-note-item {
    padding: 8px;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
  }

  .release-note-meta {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 4px;
  }

  .release-note-version {
    font-size: 12px;
    font-weight: 600;
    color: #10b981;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .release-note-date {
    font-size: 11px;
    color: var(--text-muted);
  }

  .release-note-body {
    font-size: 11px;
    color: var(--text-secondary);
    line-height: 1.5;
    margin: 4px 0 0 0;
    padding-left: 18px;
    list-style: disc;
    text-align: left;
  }

  .release-note-body li {
    margin-bottom: 2px;
    word-break: break-word;
  }

  .release-note-body-empty {
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
  }

  .release-notes-empty {
    font-size: 12px;
    color: var(--text-muted);
    font-style: italic;
    padding: 4px 0;
  }

  /* Auto-update styles */
  .auto-update-section {
    margin-bottom: 12px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border-primary);
  }

  .install-method-info {
    font-size: 12px;
    color: var(--text-secondary);
    margin-bottom: 8px;
  }

  .install-method-info strong {
    color: var(--text-primary);
  }

  .detect-loading {
    color: var(--text-muted);
    font-style: italic;
  }

  .auto-update-btn {
    width: 100%;
    padding: 8px 16px;
    background-color: #10b981;
    color: #fff;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
    transition: background-color 0.2s;
  }

  .auto-update-btn:hover:not(:disabled) {
    background-color: #059669;
  }

  .auto-update-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .auto-update-btn.retry {
    margin-top: 8px;
    background-color: var(--bg-primary);
    color: var(--text-secondary);
    border: 1px solid var(--border-primary);
  }

  .auto-update-btn.retry:hover {
    background-color: var(--border-primary);
    color: var(--text-primary);
  }

  .update-progress {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text-secondary);
    padding: 8px 0;
  }

  .spinner {
    display: inline-block;
    width: 16px;
    height: 16px;
    border: 2px solid var(--border-primary);
    border-top-color: #10b981;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  .update-success {
    font-size: 13px;
    color: #10b981;
    padding: 8px 0;
    font-weight: 500;
  }

  .update-success strong {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .update-error {
    font-size: 12px;
    color: #ef4444;
    padding: 8px 0;
  }

  .update-log {
    margin-top: 6px;
    font-size: 11px;
    color: var(--text-secondary);
  }

  .update-log summary {
    cursor: pointer;
    color: var(--text-muted);
    margin-bottom: 4px;
  }

  .update-log pre {
    background-color: var(--bg-primary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    padding: 8px;
    max-height: 120px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 10px;
    margin: 0;
  }
</style>
