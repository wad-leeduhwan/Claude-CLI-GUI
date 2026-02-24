<script>
  import { createEventDispatcher, tick } from 'svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs, RemoveTab, ToggleTabAdminMode, SetWorkDir, CompletePath, AddContextFile, RemoveContextFile, GetContextFileContent, GetAutoContextFiles, SelectFiles, ConnectWorkerTab, DisconnectWorkerTab, GetAvailableModels, GetCurrentModel, SetModel } from '../../../wailsjs/go/main/App';
  import { orchestratorStore } from '../stores/orchestrator.js';
  import { modelStore } from '../stores/model.js';

  export let node;
  export let tabs = [];

  const dispatch = createEventDispatcher();

  let resizing = null; // { index, startPos, startSizes, direction, containerEl }
  let draggingOver = null; // 'top', 'bottom', 'left', 'right', 'center'
  let dragOverTimer = null;

  // Context file panel state
  let contextExpanded = false;
  let contextPreview = null; // { path, content }
  let autoContextFiles = []; // auto-detected .claude files

  function toggleContext() {
    contextExpanded = !contextExpanded;
    if (!contextExpanded) {
      contextPreview = null;
    }
  }

  async function addContextFile() {
    if (!tab) return;
    try {
      const files = await SelectFiles();
      if (!files || files.length === 0) return;
      for (const f of files) {
        try {
          await AddContextFile(tab.id, f);
        } catch (e) {
          console.warn('Failed to add context file:', e);
        }
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to add context file:', error);
    }
  }

  async function removeContextFile(path) {
    if (!tab) return;
    try {
      await RemoveContextFile(tab.id, path);
      if (contextPreview && contextPreview.path === path) {
        contextPreview = null;
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to remove context file:', error);
    }
  }

  function getContextBasePath(path, name) {
    const cleanName = name.startsWith('~/') ? name.slice(2) : name;
    if (path.endsWith(cleanName)) {
      return path.slice(0, path.length - cleanName.length - 1);
    }
    return path;
  }

  async function previewContextFile(path) {
    try {
      const content = await GetContextFileContent(path);
      contextPreview = { path, content };
    } catch (error) {
      console.error('Failed to preview context file:', error);
      contextPreview = { path, content: '파일을 읽을 수 없습니다.' };
    }
  }

  // Worker connection helpers
  function isConnected(workerTabId) {
    if (!tab || !tab.orchestrator) return false;
    return (tab.orchestrator.connectedTabs || []).includes(workerTabId);
  }

  async function toggleWorkerConnection(workerTabId) {
    if (!tab || !tab.adminMode || !tab.orchestrator) return;
    try {
      if (isConnected(workerTabId)) {
        await DisconnectWorkerTab(tab.id, workerTabId);
      } else {
        await ConnectWorkerTab(tab.id, workerTabId);
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to toggle worker connection:', error);
    }
  }

  // Check if a tab is actively working as a worker
  function isWorkerActive(tabId) {
    const state = $orchestratorStore;
    for (const [adminId, adminState] of Object.entries(state)) {
      if (adminState.tasks) {
        for (const task of Object.values(adminState.tasks)) {
          if (task.workerTabId === tabId && task.status === 'running') {
            return true;
          }
        }
      }
    }
    return false;
  }

  $: workerActive = tab ? isWorkerActive(tab.id) : false;

  // WorkDir editing state
  let editingWorkDir = false;
  let workDirInput = '';
  let workDirInputEl;
  let completions = [];
  let completionIndex = -1;
  let completionVisible = false;

  function startEditWorkDir() {
    if (!tab) return;
    editingWorkDir = true;
    workDirInput = tab.workDir || '';
    completions = [];
    completionIndex = -1;
    completionVisible = false;
    tick().then(() => {
      if (workDirInputEl) {
        workDirInputEl.focus();
        workDirInputEl.select();
      }
    });
  }

  async function commitWorkDir() {
    if (!tab || !workDirInput.trim()) {
      cancelEditWorkDir();
      return;
    }
    try {
      await SetWorkDir(tab.id, workDirInput.trim());
      dispatch('update');
    } catch (error) {
      console.error('Failed to set workdir:', error);
    }
    editingWorkDir = false;
    completions = [];
    completionVisible = false;
  }

  function cancelEditWorkDir() {
    editingWorkDir = false;
    completions = [];
    completionVisible = false;
  }

  async function handleWorkDirTab(event) {
    if (event.key === 'Tab') {
      event.preventDefault();

      // If completion popup visible and item selected, apply it
      if (completionVisible && completions.length > 0 && completionIndex >= 0) {
        workDirInput = completions[completionIndex];
        completions = [];
        completionVisible = false;
        completionIndex = -1;
        // Fetch next level completions
        await fetchCompletions();
        return;
      }

      // Fetch completions for current input
      await fetchCompletions();

      // If exactly one match, auto-apply
      if (completions.length === 1) {
        workDirInput = completions[0];
        completions = [];
        completionVisible = false;
        // Fetch next level
        await fetchCompletions();
      } else if (completions.length > 1) {
        completionIndex = 0;
        completionVisible = true;
        // Apply common prefix
        const common = getCommonPrefix(completions);
        if (common.length > workDirInput.length) {
          workDirInput = common;
        }
      }
      return;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      if (completionVisible && completions.length > 0 && completionIndex >= 0) {
        workDirInput = completions[completionIndex];
        completions = [];
        completionVisible = false;
        completionIndex = -1;
      } else {
        commitWorkDir();
      }
      return;
    }

    if (event.key === 'Escape') {
      event.preventDefault();
      if (completionVisible) {
        completions = [];
        completionVisible = false;
      } else {
        cancelEditWorkDir();
      }
      return;
    }

    if (completionVisible) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        completionIndex = (completionIndex + 1) % completions.length;
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        completionIndex = (completionIndex - 1 + completions.length) % completions.length;
        return;
      }
    }

    // Hide completions on normal typing
    completionVisible = false;
    completionIndex = -1;
  }

  async function fetchCompletions() {
    try {
      const results = await CompletePath(workDirInput);
      // Only show directories
      completions = results.filter(p => p.endsWith('/'));
    } catch {
      completions = [];
    }
  }

  function getCommonPrefix(strings) {
    if (strings.length === 0) return '';
    let prefix = strings[0];
    for (let i = 1; i < strings.length; i++) {
      while (!strings[i].startsWith(prefix)) {
        prefix = prefix.slice(0, -1);
      }
    }
    return prefix;
  }

  function shortenPath(p) {
    if (!p) return '';
    const home = '~';
    // Try to shorten home directory prefix
    const parts = p.split('/');
    if (parts.length > 3 && p.startsWith('/Users/')) {
      return '~/' + parts.slice(3).join('/');
    }
    return p;
  }

  async function handleCloseTab(tabId) {
    try {
      await RemoveTab(tabId);
      dispatch('close', { tabId });
    } catch (error) {
      console.error('Failed to close tab:', error);
    }
  }

  function handleDragStart(event, tabId) {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', tabId);
  }

  function handleDragOver(event) {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';

    // Calculate drop zone based on cursor position
    const rect = event.currentTarget.getBoundingClientRect();
    const x = event.clientX - rect.left;
    const y = event.clientY - rect.top;
    const w = rect.width;
    const h = rect.height;

    const threshold = 0.25; // 25% from edge

    if (y < h * threshold) {
      draggingOver = 'top';
    } else if (y > h * (1 - threshold)) {
      draggingOver = 'bottom';
    } else if (x < w * threshold) {
      draggingOver = 'left';
    } else if (x > w * (1 - threshold)) {
      draggingOver = 'right';
    } else {
      draggingOver = 'center';
    }
  }

  function handleDragLeave(event) {
    clearTimeout(dragOverTimer);
    dragOverTimer = setTimeout(() => {
      draggingOver = null;
    }, 50);
  }

  function handleDrop(event) {
    event.preventDefault();
    const draggedTabId = event.dataTransfer.getData('text/plain');

    if (!draggedTabId || !node.tabId) {
      draggingOver = null;
      return;
    }

    // Notify parent to handle the split
    dispatch('split', {
      targetNode: node,
      draggedTabId,
      direction: draggingOver
    });

    draggingOver = null;
  }

  function startResize(index, direction, event) {
    event.preventDefault();
    const containerEl = event.target.parentElement;
    const children = node.children;
    resizing = {
      index,
      startPos: direction === 'horizontal' ? event.clientX : event.clientY,
      startSizes: children.map(c => c.size || 1),
      direction,
      containerEl
    };
    document.body.classList.add('resizing');
  }

  function handleResizeMove(event) {
    if (!resizing) return;
    const { index, startPos, startSizes, direction, containerEl } = resizing;
    const currentPos = direction === 'horizontal' ? event.clientX : event.clientY;
    const containerSize = direction === 'horizontal' ? containerEl.offsetWidth : containerEl.offsetHeight;
    if (containerSize === 0) return;

    const totalFlex = startSizes.reduce((a, b) => a + b, 0);
    const deltaPx = currentPos - startPos;
    const deltaFlex = (deltaPx / containerSize) * totalFlex;

    const minSize = totalFlex * 0.1;
    let newLeft = startSizes[index] + deltaFlex;
    let newRight = startSizes[index + 1] - deltaFlex;

    if (newLeft < minSize) {
      newLeft = minSize;
      newRight = startSizes[index] + startSizes[index + 1] - minSize;
    }
    if (newRight < minSize) {
      newRight = minSize;
      newLeft = startSizes[index] + startSizes[index + 1] - minSize;
    }

    node.children[index].size = newLeft;
    node.children[index + 1].size = newRight;
    node = node; // trigger reactivity
  }

  function stopResize() {
    if (!resizing) return;
    resizing = null;
    document.body.classList.remove('resizing');
  }

  // Reactive statement that depends on tabs array changes
  $: tab = node.tabId && tabs ? tabs.find(t => t.id === node.tabId) : null;
  $: if (tab) {
    console.log(`[SplitPane] Tab ${tab.id} updated with ${tab.messages?.length || 0} messages`);
  }

  // Fetch auto context files whenever tab or workDir changes
  $: if (tab && tab.workDir) {
    GetAutoContextFiles(tab.workDir).then(files => {
      autoContextFiles = files || [];
    }).catch(() => {
      autoContextFiles = [];
    });
  }

  // Model popup state
  let modelPopupOpen = false;
  let modelPopupModels = [];
  let modelPopupCurrent = '';
  let modelPopupSelectedIndex = 0;

  async function openModelPopup() {
    try {
      const [models, current] = await Promise.all([GetAvailableModels(), GetCurrentModel()]);
      modelPopupModels = models;
      modelPopupCurrent = current;
      modelPopupSelectedIndex = Math.max(0, models.indexOf(current));
      modelPopupOpen = true;
      // Close popup on outside click
      setTimeout(() => {
        function onClickOutside(e) {
          if (!e.target.closest('.model-badge-wrapper')) {
            modelPopupOpen = false;
            window.removeEventListener('click', onClickOutside, true);
          }
        }
        window.addEventListener('click', onClickOutside, true);
      }, 0);
    } catch (e) { console.error('Failed to load models:', e); }
  }

  async function selectModelFromPopup(model) {
    try {
      await SetModel(model);
      modelStore.set(model);
      modelPopupOpen = false;
    } catch (e) { console.error('Failed to set model:', e); }
  }

  function handleModelPopupKeydown(event) {
    if (!modelPopupOpen) return;
    if (event.key === 'ArrowDown') { event.preventDefault(); modelPopupSelectedIndex = (modelPopupSelectedIndex + 1) % modelPopupModels.length; }
    if (event.key === 'ArrowUp') { event.preventDefault(); modelPopupSelectedIndex = (modelPopupSelectedIndex - 1 + modelPopupModels.length) % modelPopupModels.length; }
    if (event.key === 'Enter') { event.preventDefault(); selectModelFromPopup(modelPopupModels[modelPopupSelectedIndex]); }
    if (event.key === 'Escape') { event.preventDefault(); modelPopupOpen = false; }
  }

</script>

<svelte:window on:mousemove={handleResizeMove} on:mouseup={stopResize} on:keydown={handleModelPopupKeydown} />

{#if node.type === 'container'}
  <div class="split-container" class:horizontal={node.direction === 'horizontal'} class:vertical={node.direction === 'vertical'}>
    {#each node.children as child, i}
      {#if i > 0}
        <div class="splitter" class:horizontal={node.direction === 'horizontal'} class:vertical={node.direction === 'vertical'} class:active={resizing && resizing.index === i - 1} on:mousedown={(e) => startResize(i - 1, node.direction, e)}></div>
      {/if}
      <div class="split-child" style="flex: {child.size || 1};">
        <svelte:self node={child} {tabs} on:split on:update on:close />
      </div>
    {/each}
  </div>
{:else if node.type === 'leaf' && tab}
  <div
    class="panel"
    class:admin-mode={tab.adminMode}
    class:plan-mode={tab.planMode}
    class:worker-active={workerActive}
    on:dragover={handleDragOver}
    on:dragleave={handleDragLeave}
    on:drop={handleDrop}
  >
    <div
      class="panel-header"
      draggable="true"
      on:dragstart={(e) => handleDragStart(e, tab.id)}
    >
      <span class="panel-title">{tab.name}</span>
      {#if $modelStore}
        <div class="model-badge-wrapper" draggable="false" on:mousedown|stopPropagation on:dragstart|preventDefault|stopPropagation>
          <button class="model-badge" on:click|stopPropagation={openModelPopup} title="모델 변경: {$modelStore}">{$modelStore}</button>
          {#if modelPopupOpen}
            <div class="model-popup">
              {#each modelPopupModels as model, i}
                <div class="model-popup-item" class:selected={i === modelPopupSelectedIndex} on:click|stopPropagation={() => selectModelFromPopup(model)}>
                  {model}
                  {#if model === modelPopupCurrent}<span class="model-popup-current">현재</span>{/if}
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
      <div class="workdir-area">
        {#if editingWorkDir}
          <div class="workdir-edit-wrapper">
            <input
              bind:this={workDirInputEl}
              bind:value={workDirInput}
              on:keydown={handleWorkDirTab}
              on:blur={() => { setTimeout(cancelEditWorkDir, 150); }}
              class="workdir-input"
              spellcheck="false"
            />
            {#if completionVisible && completions.length > 0}
              <div class="workdir-completions">
                {#each completions as comp, i}
                  <div
                    class="completion-item"
                    class:selected={i === completionIndex}
                    on:mousedown|preventDefault={() => { workDirInput = comp; completionVisible = false; }}
                  >
                    {comp}
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {:else}
          <button class="workdir-display" on:click={startEditWorkDir} title="작업 디렉토리 변경">
            {shortenPath(tab.workDir)}
          </button>
        {/if}
      </div>
      <button
        class="context-toggle"
        on:click|stopPropagation={toggleContext}
        title="컨텍스트 파일 관리"
      >
        CTX {(autoContextFiles?.length || 0) + (tab.contextFiles?.length || 0)}
      </button>
      <button
        class="close-btn"
        on:click={() => handleCloseTab(tab.id)}
        title="탭 닫기"
      >
        ✕
      </button>
    </div>

    {#if tab.adminMode && tab.orchestrator}
      <div class="worker-connections">
        <span class="worker-connections-label">워커:</span>
        {#each tabs.filter(t => !t.adminMode && t.id !== tab.id) as wTab}
          <button
            class="worker-chip"
            class:connected={isConnected(wTab.id)}
            on:click|stopPropagation={() => toggleWorkerConnection(wTab.id)}
            title={isConnected(wTab.id) ? '연결 해제' : '연결'}
          >
            {wTab.name}
          </button>
        {/each}
      </div>
    {/if}

    {#if contextExpanded}
      <div class="context-panel">
        <!-- Auto-detected .claude files -->
        {#if autoContextFiles.length > 0}
          <div class="context-section">
            <div class="context-section-label">자동 참조 (Claude CLI)</div>
            <div class="context-files">
              {#each autoContextFiles as af}
                <div class="context-file-item auto">
                  <span class="context-scope-badge" class:global={af.scope === 'global'}>{af.scope === 'global' ? 'G' : 'P'}</span>
                  <span class="context-file-name" on:click={() => previewContextFile(af.path)}>
                    {af.name}
                  </span>
                  <span class="context-file-path">{getContextBasePath(af.path, af.name)}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- User-added context files -->
        <div class="context-section">
          <div class="context-section-label">수동 컨텍스트 파일</div>
          <div class="context-files">
            {#each tab.contextFiles || [] as cf}
              <div class="context-file-item">
                <span class="context-file-name" on:click={() => previewContextFile(cf)}>
                  {shortenPath(cf)}
                </span>
                <button class="context-remove-btn" on:click={() => removeContextFile(cf)} title="제거">✕</button>
              </div>
            {/each}
          </div>
          <button class="context-add-btn" on:click={addContextFile}>+ 파일 추가</button>
        </div>

        {#if contextPreview}
          <div class="context-preview">
            <div class="context-preview-header">
              <span class="context-preview-path">{shortenPath(contextPreview.path)}</span>
              <button class="context-preview-close" on:click={() => contextPreview = null}>닫기</button>
            </div>
            <pre class="context-preview-content">{contextPreview.content}</pre>
          </div>
        {/if}
      </div>
    {/if}

    <div class="panel-content">
      {#key tab.id}
        <ConversationTab
          tabId={tab.id}
          tabName={tab.name}
          messages={tab.messages}
          adminMode={tab.adminMode}
          planMode={tab.planMode}
          workDir={tab.workDir}
          on:refresh={() => {
            console.log('[SplitPane] Received refresh event, dispatching update');
            dispatch('update');
          }}
        />
      {/key}
    </div>

    {#if draggingOver}
      <div class="drop-overlay">
        <div class="drop-zone" class:active={draggingOver === 'top'} style="top: 0; left: 0; right: 0; height: 25%;"></div>
        <div class="drop-zone" class:active={draggingOver === 'bottom'} style="bottom: 0; left: 0; right: 0; height: 25%;"></div>
        <div class="drop-zone" class:active={draggingOver === 'left'} style="top: 0; left: 0; bottom: 0; width: 25%;"></div>
        <div class="drop-zone" class:active={draggingOver === 'right'} style="top: 0; right: 0; bottom: 0; width: 25%;"></div>
        <div class="drop-zone" class:active={draggingOver === 'center'} style="top: 25%; left: 25%; right: 25%; bottom: 25%;"></div>
      </div>
    {/if}
  </div>
{/if}

<style>
  .split-container {
    display: flex;
    width: 100%;
    height: 100%;
  }

  .split-container.horizontal {
    flex-direction: row;
  }

  .split-container.vertical {
    flex-direction: column;
  }

  .split-child {
    min-width: 0;
    min-height: 0;
    display: flex;
  }

  .splitter {
    background-color: var(--border-primary);
    flex-shrink: 0;
  }

  .splitter.horizontal {
    width: 4px;
    cursor: col-resize;
  }

  .splitter.vertical {
    height: 4px;
    cursor: row-resize;
  }

  .splitter:hover,
  .splitter.active {
    background-color: var(--accent);
  }

  :global(body.resizing) {
    user-select: none;
  }

  :global(body.resizing .split-container.horizontal) {
    cursor: col-resize;
  }

  :global(body.resizing .split-container.vertical) {
    cursor: row-resize;
  }

  .panel {
    display: flex;
    flex-direction: column;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    width: 100%;
    height: 100%;
    position: relative;
  }

  .panel.admin-mode {
    border-color: #ff8c00;
  }

  .panel.plan-mode {
    border-color: #7c3aed;
  }

  .panel.plan-mode .panel-header {
    background-color: rgba(124, 58, 237, 0.08);
  }

  .panel-header {
    display: flex;
    align-items: center;
    padding: 6px 12px;
    background-color: var(--bg-header);
    border-bottom: 1px solid var(--border-primary);
    color: var(--text-primary);
    font-size: 13px;
    flex-shrink: 0;
    gap: 4px;
    cursor: grab;
    position: relative;
  }

  .panel-header:active {
    cursor: grabbing;
  }

  .panel.admin-mode .panel-header {
    background-color: #fff3e0;
  }

  .panel-title {
    font-weight: 500;
    user-select: none;
    flex-shrink: 0;
  }

  .model-badge-wrapper {
    position: relative;
    flex-shrink: 0;
    cursor: default;
  }

  .model-badge {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 10px;
    color: var(--accent);
    background-color: var(--accent-bg, rgba(0, 120, 212, 0.1));
    padding: 2px 8px;
    border-radius: 10px;
    border: 1px solid rgba(0, 120, 212, 0.2);
    max-width: 150px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex-shrink: 0;
    user-select: none;
    cursor: pointer;
    transition: all 0.15s;
  }

  .model-badge:hover {
    border-color: var(--accent);
    background-color: rgba(0, 120, 212, 0.15);
  }

  .model-popup {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 200;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    box-shadow: var(--shadow-md, 0 4px 12px rgba(0,0,0,0.3));
    min-width: 220px;
    max-height: 300px;
    overflow-y: auto;
    margin-top: 2px;
  }

  .model-popup-item {
    padding: 6px 10px;
    cursor: pointer;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: var(--text-primary);
    display: flex;
    align-items: center;
    gap: 6px;
    transition: background-color 0.1s;
  }

  .model-popup-item:hover {
    background-color: var(--bg-hover);
  }

  .model-popup-item.selected {
    background-color: var(--accent);
    color: var(--text-inverse);
  }

  .model-popup-current {
    font-size: 9px;
    font-weight: 600;
    color: #10b981;
    margin-left: auto;
  }

  .model-popup-item.selected .model-popup-current {
    color: var(--text-inverse);
  }

  .workdir-area {
    flex: 1;
    min-width: 0;
    margin: 0 8px;
    position: relative;
  }

  .workdir-display {
    background: none;
    border: none;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 3px;
    width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
    text-align: left;
    transition: background-color 0.15s, color 0.15s;
    min-height: 24px;
    line-height: 16px;
  }

  .workdir-display:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
  }

  .workdir-edit-wrapper {
    position: relative;
  }

  .workdir-input {
    width: 100%;
    background-color: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--accent);
    border-radius: 3px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    padding: 2px 6px;
    outline: none;
    box-sizing: border-box;
  }

  .workdir-completions {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 0 0 4px 4px;
    max-height: 200px;
    overflow-y: auto;
    z-index: 100;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    box-shadow: var(--shadow-md);
  }

  .completion-item {
    padding: 4px 8px;
    color: var(--text-primary);
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .completion-item:hover {
    background-color: var(--bg-header);
  }

  .completion-item.selected {
    background-color: var(--accent);
    color: var(--text-inverse);
  }

  .close-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 2px 6px;
    font-size: 16px;
    line-height: 1;
    border-radius: 3px;
    transition: all 0.2s;
  }

  .close-btn:hover {
    background-color: var(--error);
    color: var(--text-inverse);
  }

  .panel-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  .drop-overlay {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    pointer-events: none;
    z-index: 1000;
  }

  .drop-zone {
    position: absolute;
    pointer-events: none;
    transition: background-color 0.2s;
  }

  .drop-zone.active {
    background-color: var(--drag-overlay);
    border: 2px solid var(--accent);
  }

  /* Context file panel styles */
  .context-toggle {
    background: none;
    border: 1px solid var(--border-primary);
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 600;
    padding: 2px 6px;
    border-radius: 8px;
    cursor: pointer;
    white-space: nowrap;
    flex-shrink: 0;
    transition: all 0.15s;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .context-toggle:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
    border-color: var(--accent);
  }

  .context-panel {
    border-bottom: 1px solid var(--border-primary);
    background-color: var(--bg-header);
    padding: 8px 12px;
    max-height: 300px;
    overflow-y: auto;
    flex-shrink: 0;
  }

  .context-section {
    margin-bottom: 8px;
  }

  .context-section-label {
    font-size: 10px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 4px;
    padding-left: 2px;
  }

  .context-scope-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 700;
    flex-shrink: 0;
    background-color: var(--accent);
    color: var(--text-inverse);
  }

  .context-scope-badge.global {
    background-color: var(--text-muted);
  }

  .context-file-item.auto {
    opacity: 0.85;
  }

  .context-file-item.auto {
    position: relative;
  }

  .context-file-path {
    position: absolute;
    right: 6px;
    top: 50%;
    transform: translateY(-50%);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 10px;
    color: var(--text-muted);
    opacity: 0.5;
    max-width: 60%;
    direction: rtl;
    text-align: right;
  }

  .context-files {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .context-file-item {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 3px 6px;
    border-radius: 4px;
    transition: background-color 0.1s;
  }

  .context-file-item:hover {
    background-color: var(--bg-hover);
  }

  .context-file-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: var(--text-primary);
    cursor: pointer;
  }

  .context-file-name:hover {
    text-decoration: underline;
    color: var(--accent);
  }

  .context-remove-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 1px 4px;
    font-size: 12px;
    border-radius: 3px;
    flex-shrink: 0;
    transition: all 0.15s;
  }

  .context-remove-btn:hover {
    background-color: var(--error);
    color: var(--text-inverse);
  }

  .context-add-btn {
    background: none;
    border: 1px dashed var(--border-primary);
    color: var(--text-muted);
    font-size: 11px;
    padding: 4px 8px;
    border-radius: 4px;
    cursor: pointer;
    width: 100%;
    margin-top: 6px;
    transition: all 0.15s;
  }

  .context-add-btn:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
    border-color: var(--accent);
  }

  .context-preview {
    margin-top: 8px;
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    overflow: hidden;
  }

  .context-preview-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 8px;
    background-color: var(--bg-secondary);
    border-bottom: 1px solid var(--border-primary);
    font-size: 11px;
  }

  .context-preview-path {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .context-preview-close {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 11px;
    padding: 2px 6px;
    border-radius: 3px;
    flex-shrink: 0;
  }

  .context-preview-close:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
  }

  .context-preview-content {
    max-height: 200px;
    overflow-y: auto;
    padding: 8px;
    margin: 0;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: var(--text-primary);
    background-color: var(--bg-primary, #1a1a2e);
    white-space: pre-wrap;
    word-break: break-all;
  }

  /* Worker active state */
  .panel.worker-active {
    border-color: #f59e0b;
  }

  .panel.worker-active .panel-header {
    background-color: rgba(245, 158, 11, 0.08);
  }

  /* Worker connections bar */
  .worker-connections {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 12px;
    border-bottom: 1px solid var(--border-primary);
    background-color: rgba(255, 140, 0, 0.05);
    flex-shrink: 0;
    flex-wrap: wrap;
  }

  .worker-connections-label {
    font-size: 10px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    flex-shrink: 0;
    margin-right: 4px;
  }

  .worker-chip {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 12px;
    font-size: 11px;
    font-weight: 500;
    border: 1px solid var(--border-primary);
    background-color: var(--bg-secondary);
    color: var(--text-muted);
    cursor: pointer;
    transition: all 0.15s;
    white-space: nowrap;
  }

  .worker-chip:hover {
    border-color: var(--accent);
    color: var(--text-primary);
  }

  .worker-chip.connected {
    background-color: rgba(16, 185, 129, 0.15);
    border-color: #10b981;
    color: #10b981;
    font-weight: 600;
  }

  .worker-chip.connected:hover {
    background-color: rgba(239, 68, 68, 0.1);
    border-color: #ef4444;
    color: #ef4444;
  }
</style>
