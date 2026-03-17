<script>
  import { createEventDispatcher, tick } from 'svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs, RemoveTab, ToggleTabAdminMode, SetWorkDir, CompletePath, AddContextFile, RemoveContextFile, GetContextFileContent, SaveFileContent, GetAutoContextFiles, SelectFiles, ConnectWorkerTab, DisconnectWorkerTab, ConnectTeamsWorker, DisconnectTeamsWorker, GetAvailableModels, GetCurrentModel, SetModel, GetAvailableSessions, SwitchTabSession, GetTabSessionID, DeleteSession, CreateClaudeMd, CreateTabClaudeMd, RenameFile, DeleteFile, RenameTab, RequestCodeReview, GetPlanContent, GetTeamPresets, SaveTeamPreset, ApplyTeamPreset, DeleteTeamPreset, RenameTeamPreset, ResumeSessionInTerminal } from '../../../wailsjs/go/main/App';
  import MarkdownViewer from './MarkdownViewer.svelte';
  import { orchestratorStore } from '../stores/orchestrator.js';
  import { modelStore } from '../stores/model.js';
  import { agentStore } from '../stores/agent.js';
  import { streamingStore } from '../stores/streaming.js';

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

  // Editor panel state
  let editorFile = null;       // { path, content, originalContent }
  let editorDirty = false;
  let editorCloseConfirm = false;
  let editorSaving = false;
  let editorSplitRatio = [1, 1]; // [conversation flex, editor flex]
  let editorResizing = null;
  let editorLineNumbers = '';
  let editorTextarea = null;
  let editorLineNumbersEl = null;
  let editorRenaming = false;
  let editorRenameInput = '';
  let editorRenameInputEl = null;

  function isTeamsWorkerBusy(tabId) {
    return tabs.some(t =>
      t.teamsMode &&
      t.teamsState?.connectedTabs?.includes(tabId) &&
      $streamingStore[t.id]?.isStreaming
    );
  }

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
      if (editorFile && editorFile.path === path) {
        editorFile = null;
        editorDirty = false;
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to remove context file:', error);
    }
  }

  async function deleteFileFromDisk(path) {
    try {
      await DeleteFile(path);
      // Close editor if the deleted file was open
      if (editorFile && editorFile.path === path) {
        editorFile = null;
        editorDirty = false;
      }
      // Close preview if the deleted file was being previewed
      if (contextPreview && contextPreview.path === path) {
        contextPreview = null;
      }
      // Refresh auto context files
      if (tab) {
        autoContextFiles = (await GetAutoContextFiles(tab.workDir)) || [];
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to delete file:', error);
      alert('파일 삭제 실패: ' + error);
    }
  }

  async function createClaudeMd() {
    if (!tab) return;
    try {
      const filePath = await CreateClaudeMd(tab.workDir);
      autoContextFiles = (await GetAutoContextFiles(tab.workDir)) || [];
      openFileInEditor(filePath);
    } catch (error) {
      console.error('Failed to create CLAUDE.md:', error);
    }
  }

  // Agent: accept tab rename suggestion
  async function acceptRename() {
    if (!tab) return;
    const suggested = $agentStore.tabRenames[tab.id];
    if (!suggested) return;
    try {
      await RenameTab(tab.id, suggested);
      agentStore.clearTabRename(tab.id);
      dispatch('update');
    } catch (error) {
      console.error('Failed to rename tab:', error);
    }
  }

  function dismissRename() {
    if (!tab) return;
    agentStore.clearTabRename(tab.id);
  }

  // Inline tab name editing
  let editingTabName = false;
  let tabNameInput = '';
  let tabNameInputEl;

  function startEditTabName() {
    if (!tab) return;
    editingTabName = true;
    tabNameInput = tab.name;
    tick().then(() => {
      if (tabNameInputEl) {
        tabNameInputEl.focus();
        tabNameInputEl.select();
      }
    });
  }

  async function commitTabName() {
    editingTabName = false;
    if (!tab || !tabNameInput.trim() || tabNameInput.trim() === tab.name) return;
    try {
      await RenameTab(tab.id, tabNameInput.trim());
      dispatch('update');
    } catch (error) {
      console.error('Failed to rename tab:', error);
    }
  }

  function handleTabNameKeydown(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      commitTabName();
    } else if (e.key === 'Escape') {
      editingTabName = false;
    }
  }

  async function createTabClaudeMd() {
    if (!tab) return;
    try {
      const filePath = await CreateTabClaudeMd(tab.id);
      dispatch('update');
      openFileInEditor(filePath);
    } catch (error) {
      console.error('Failed to create tab CLAUDE.md:', error);
    }
  }

  function dismissCodeReview() {
    if (!tab) return;
    agentStore.clearCodeReview(tab.id);
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

  async function openFileInEditor(path) {
    // If there's an unsaved file, confirm before switching
    if (editorFile && editorDirty) {
      const confirmed = confirm('현재 파일에 저장하지 않은 변경사항이 있습니다. 다른 파일을 여시겠습니까?');
      if (!confirmed) return;
    }
    try {
      const content = await GetContextFileContent(path);
      editorFile = { path, content, originalContent: content };
      editorDirty = false;
      updateLineNumbers(content);
      await tick();
      if (editorTextarea) {
        editorTextarea.scrollTop = 0;
      }
    } catch (error) {
      console.error('Failed to open file in editor:', error);
    }
  }

  function closeEditor() {
    if (editorDirty && !editorCloseConfirm) {
      editorCloseConfirm = true;
      return;
    }
    editorCloseConfirm = false;
    editorFile = null;
    editorDirty = false;
    editorSplitRatio = [1, 1];
  }

  function cancelCloseEditor() {
    editorCloseConfirm = false;
  }

  async function saveEditorFile() {
    if (!editorFile || editorSaving) return;
    editorSaving = true;
    try {
      await SaveFileContent(editorFile.path, editorFile.content);
      editorFile.originalContent = editorFile.content;
      editorDirty = false;
    } catch (error) {
      console.error('Failed to save file:', error);
      alert('파일 저장 실패: ' + error);
    } finally {
      editorSaving = false;
    }
  }

  function startEditorRename() {
    if (!editorFile) return;
    editorRenaming = true;
    editorRenameInput = editorFile.path;
    tick().then(() => {
      if (editorRenameInputEl) {
        editorRenameInputEl.focus();
        // Select only the filename portion
        const lastSlash = editorRenameInput.lastIndexOf('/');
        const lastDot = editorRenameInput.lastIndexOf('.');
        const selStart = lastSlash + 1;
        const selEnd = lastDot > selStart ? lastDot : editorRenameInput.length;
        editorRenameInputEl.setSelectionRange(selStart, selEnd);
      }
    });
  }

  async function commitEditorRename() {
    if (!editorFile || !editorRenameInput.trim()) {
      cancelEditorRename();
      return;
    }
    const newPath = editorRenameInput.trim();
    if (newPath === editorFile.path) {
      cancelEditorRename();
      return;
    }
    try {
      // Save unsaved content first
      if (editorDirty) {
        await SaveFileContent(editorFile.path, editorFile.content);
        editorFile.originalContent = editorFile.content;
        editorDirty = false;
      }
      await RenameFile(editorFile.path, newPath);
      editorFile.path = newPath;
      // Refresh auto context files in case a .claude file was renamed
      if (tab) {
        autoContextFiles = (await GetAutoContextFiles(tab.workDir)) || [];
      }
    } catch (error) {
      console.error('Failed to rename file:', error);
      alert('파일 이름 변경 실패: ' + error);
    }
    editorRenaming = false;
  }

  function cancelEditorRename() {
    editorRenaming = false;
  }

  function handleEditorRenameKeydown(event) {
    if (event.key === 'Enter') {
      event.preventDefault();
      commitEditorRename();
    } else if (event.key === 'Escape') {
      event.preventDefault();
      cancelEditorRename();
    }
  }

  function handleEditorInput(event) {
    editorFile.content = event.target.value;
    editorDirty = editorFile.content !== editorFile.originalContent;
    updateLineNumbers(editorFile.content);
  }

  function handleEditorKeydown(event) {
    if ((event.metaKey || event.ctrlKey) && event.key === 's') {
      event.preventDefault();
      saveEditorFile();
    }
    if (event.key === 'Tab') {
      event.preventDefault();
      const textarea = event.target;
      const start = textarea.selectionStart;
      const end = textarea.selectionEnd;
      const value = textarea.value;
      textarea.value = value.substring(0, start) + '  ' + value.substring(end);
      textarea.selectionStart = textarea.selectionEnd = start + 2;
      editorFile.content = textarea.value;
      editorDirty = editorFile.content !== editorFile.originalContent;
      updateLineNumbers(editorFile.content);
    }
  }

  function handleEditorScroll(event) {
    if (editorLineNumbersEl) {
      editorLineNumbersEl.scrollTop = event.target.scrollTop;
    }
  }

  function updateLineNumbers(content) {
    const lines = (content || '').split('\n').length;
    editorLineNumbers = Array.from({ length: lines }, (_, i) => i + 1).join('\n');
  }

  // Editor splitter resize
  function startEditorResize(event) {
    event.preventDefault();
    const container = event.target.parentElement;
    editorResizing = {
      startX: event.clientX,
      startRatio: [...editorSplitRatio],
      containerWidth: container.offsetWidth
    };
  }

  function handleEditorResizeMove(event) {
    if (!editorResizing) return;
    const dx = event.clientX - editorResizing.startX;
    const totalFlex = editorResizing.startRatio[0] + editorResizing.startRatio[1];
    const pxPerFlex = editorResizing.containerWidth / totalFlex;
    const deltaFlex = dx / pxPerFlex;
    let newLeft = editorResizing.startRatio[0] + deltaFlex;
    let newRight = editorResizing.startRatio[1] - deltaFlex;
    const minFlex = totalFlex * 0.15;
    if (newLeft < minFlex) { newLeft = minFlex; newRight = totalFlex - minFlex; }
    if (newRight < minFlex) { newRight = minFlex; newLeft = totalFlex - minFlex; }
    editorSplitRatio = [newLeft, newRight];
  }

  function stopEditorResize() {
    editorResizing = null;
  }

  // Worker connection helpers (Admin mode)
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

  // Teams: get parent team tab name for a worker tab
  function getTeamsParentName(tabId) {
    const parent = tabs.find(t =>
      t.teamsMode && t.teamsState?.connectedTabs?.includes(tabId)
    );
    return parent ? parent.name : null;
  }

  // Teams worker connection helpers
  function isTeamsConnected(workerTabId) {
    if (!tab || !tab.teamsState) return false;
    return (tab.teamsState.connectedTabs || []).includes(workerTabId);
  }

  async function toggleTeamsWorkerConnection(workerTabId) {
    if (!tab || !tab.teamsMode || !tab.teamsState) return;
    try {
      if (isTeamsConnected(workerTabId)) {
        await DisconnectTeamsWorker(tab.id, workerTabId);
      } else {
        await ConnectTeamsWorker(tab.id, workerTabId);
      }
      dispatch('update');
    } catch (error) {
      console.error('Failed to toggle teams worker connection:', error);
    }
  }

  // Teams preset management
  let teamPresets = [];
  let selectedPresetId = '';
  let presetNameInput = '';
  let showPresetNameInput = false;
  let presetNameInputEl;
  let editingPresetId = '';
  let editingPresetName = '';
  let editingPresetInputEl;
  let presetDropdownOpen = false;

  async function loadPresets() {
    try {
      teamPresets = await GetTeamPresets();
    } catch (error) {
      console.error('Failed to load team presets:', error);
    }
  }

  function startSavePreset() {
    if (!tab || !tab.teamsMode || !tab.teamsState) return;
    if ((tab.teamsState.connectedTabs || []).length === 0) return;
    showPresetNameInput = true;
    presetNameInput = '';
    tick().then(() => {
      if (presetNameInputEl) presetNameInputEl.focus();
    });
  }

  async function commitSavePreset() {
    showPresetNameInput = false;
    if (!tab || !presetNameInput.trim()) return;
    try {
      await SaveTeamPreset(tab.id, presetNameInput.trim());
      await loadPresets();
    } catch (error) {
      console.error('Failed to save team preset:', error);
    }
  }

  function handlePresetNameKeydown(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      commitSavePreset();
    } else if (e.key === 'Escape') {
      showPresetNameInput = false;
    }
  }

  async function onPresetSelected(presetId) {
    if (!presetId || !tab || !tab.teamsMode) return;
    try {
      selectedPresetId = presetId;
      await ApplyTeamPreset(tab.id, presetId);
      dispatch('update');
    } catch (error) {
      console.error('Failed to apply team preset:', error);
    }
  }

  async function deletePreset(presetId) {
    if (!presetId) return;
    try {
      await DeleteTeamPreset(presetId);
      if (selectedPresetId === presetId) {
        selectedPresetId = '';
      }
      await loadPresets();
    } catch (error) {
      console.error('Failed to delete team preset:', error);
    }
  }

  function startRenamePreset(presetId) {
    const id = presetId || selectedPresetId;
    if (!id) return;
    const preset = teamPresets.find(p => p.id === id);
    if (!preset) return;
    editingPresetId = id;
    editingPresetName = preset.name;
    tick().then(() => {
      if (editingPresetInputEl) {
        editingPresetInputEl.focus();
        editingPresetInputEl.select();
      }
    });
  }

  async function commitRenamePreset() {
    const id = editingPresetId;
    const name = editingPresetName.trim();
    editingPresetId = '';
    editingPresetName = '';
    if (!id || !name) return;
    try {
      await RenameTeamPreset(id, name);
      await loadPresets();
    } catch (error) {
      console.error('Failed to rename team preset:', error);
    }
  }

  function handleRenamePresetKeydown(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      commitRenamePreset();
    } else if (e.key === 'Escape') {
      editingPresetId = '';
      editingPresetName = '';
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

  // Load presets when teams mode is active
  $: if (tab && tab.teamsMode) {
    loadPresets();
  }

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

  const tabColors = ['#60a5fa', '#f472b6', '#a78bfa', '#fb923c', '#34d399', '#fbbf24', '#e879f9', '#38bdf8'];

  // Reactive statement that depends on tabs array changes
  $: tab = node.tabId && tabs ? tabs.find(t => t.id === node.tabId) : null;
  $: tabIndex = tab && tabs ? tabs.indexOf(tab) : 0;
  $: tabColor = tabColors[tabIndex % tabColors.length];
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
    if (modelPopupOpen) {
      modelPopupOpen = false;
      return;
    }
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

  // Thread (session) popup state — cached in memory
  let threadPopupOpen = false;
  let threadCachedSessions = [];
  let threadPopupCurrentSessionId = '';
  let threadPopupSelectedIndex = 0;
  let threadLoading = false;
  let threadLastWorkDir = '';
  let threadSessionTabMap = {}; // sessionId -> tabName (for "used by" display)

  // Preload sessions into cache
  async function loadSessionsCache() {
    if (!tab) return;
    threadLoading = true;
    try {
      const [sessions, currentId] = await Promise.all([
        GetAvailableSessions(tab.workDir),
        GetTabSessionID(tab.id)
      ]);
      threadCachedSessions = sessions || [];
      threadPopupCurrentSessionId = currentId || '';
      threadLastWorkDir = tab.workDir;
      // Build session→tab map from all tabs
      await buildSessionTabMap();
    } catch (e) { console.error('Failed to load sessions:', e); }
    threadLoading = false;
  }

  let threadSessionColorMap = {}; // { sessionId: color }

  async function buildSessionTabMap() {
    const map = {};
    const colorMap = {};
    for (let i = 0; i < tabs.length; i++) {
      const t = tabs[i];
      try {
        const sid = await GetTabSessionID(t.id);
        if (sid) {
          map[sid] = t.name;
          colorMap[sid] = tabColors[i % tabColors.length];
        }
      } catch {}
    }
    threadSessionTabMap = map;
    threadSessionColorMap = colorMap;
  }

  // Preload on mount and when workDir changes
  $: if (tab && tab.workDir && tab.workDir !== threadLastWorkDir) {
    loadSessionsCache();
  }

  // Rebuild session-tab map when tabs prop changes (e.g. tab rename)
  $: if (tabs && tabs.length > 0 && threadCachedSessions.length > 0) {
    buildSessionTabMap();
  }

  // Also refresh currentSessionId when tab changes
  $: if (tab && tab.id) {
    GetTabSessionID(tab.id).then(id => {
      threadPopupCurrentSessionId = id || '';
    }).catch(() => {});
  }

  let threadDeleteConfirm = null; // { sessionId, projectPath, preview }
  let threadCollapsedGroups = {}; // { [projectPath]: true/false }
  let threadSearchQuery = '';

  function toggleGroupCollapse(projectPath) {
    threadCollapsedGroups[projectPath] = !threadCollapsedGroups[projectPath];
    threadCollapsedGroups = threadCollapsedGroups; // trigger reactivity
  }

  function isSessionVisible(session, projectPath, collapsed) {
    if (!collapsed) return true;
    // Always show the current session even when collapsed
    if (session.sessionId === threadPopupCurrentSessionId) return true;
    // Always show sessions in use by other tabs
    if (threadSessionTabMap[session.sessionId]) return true;
    return false;
  }

  function openThreadPopup() {
    if (!tab) return;
    if (threadPopupOpen) {
      threadPopupOpen = false;
      return;
    }
    threadPopupSelectedIndex = 0;
    threadDeleteConfirm = null;
    threadSearchQuery = '';
    threadPopupOpen = true;
    setTimeout(() => {
      function onClickOutside(e) {
        if (!e.target.closest('.thread-badge-wrapper')) {
          threadPopupOpen = false;
          threadDeleteConfirm = null;
          window.removeEventListener('click', onClickOutside, true);
        }
      }
      window.addEventListener('click', onClickOutside, true);
    }, 0);
  }

  async function refreshSessions() {
    await loadSessionsCache();
  }

  async function selectSession(sessionId, projectPath) {
    if (!tab) return;
    try {
      await SwitchTabSession(tab.id, sessionId, projectPath || '');
      threadPopupCurrentSessionId = sessionId;
      threadPopupOpen = false;
      dispatch('update');
    } catch (e) {
      console.error('Failed to switch session:', e);
      alert(e);
    }
  }

  async function resumeSessionInTerminal(sessionId, projectPath) {
    try {
      await ResumeSessionInTerminal(sessionId, projectPath);
    } catch (e) {
      console.error('Failed to open terminal:', e);
      alert('터미널 열기 실패: ' + e);
    }
  }

  function confirmDeleteSession(sessionId, projectPath, preview) {
    threadDeleteConfirm = { sessionId, projectPath, preview };
  }

  async function executeDeleteSession() {
    if (!threadDeleteConfirm) return;
    try {
      await DeleteSession(threadDeleteConfirm.sessionId, threadDeleteConfirm.projectPath);
      threadDeleteConfirm = null;
      await loadSessionsCache();
      dispatch('update');
    } catch (e) {
      console.error('Failed to delete session:', e);
      alert(e);
    }
  }

  function cancelDeleteSession() {
    threadDeleteConfirm = null;
  }

  // Group sessions: current project first, then other projects grouped by path
  $: threadCurrentProject = threadCachedSessions.filter(s => tab && s.projectPath === tab.workDir);
  $: threadOtherByProject = (() => {
    if (!tab) return [];
    const others = threadCachedSessions.filter(s => s.projectPath !== tab.workDir);
    const groups = {};
    for (const s of others) {
      const key = s.projectPath || '(unknown)';
      if (!groups[key]) groups[key] = [];
      groups[key].push(s);
    }
    // Sort groups by most recent activity
    return Object.entries(groups).sort((a, b) => {
      const ta = a[1][0]?.lastActivity || '';
      const tb = b[1][0]?.lastActivity || '';
      return tb.localeCompare(ta);
    });
  })();
  // Search filter for sessions
  function matchSession(session, tabMap) {
    const q = threadSearchQuery.toLowerCase();
    if ((session.preview || '').toLowerCase().includes(q)) return true;
    const tabName = tabMap[session.sessionId];
    if (tabName && tabName.toLowerCase().includes(q)) return true;
    return false;
  }

  $: threadFilteredCurrentProject = threadSearchQuery
    ? threadCurrentProject.filter(s => matchSession(s, threadSessionTabMap))
    : threadCurrentProject;

  $: threadFilteredOtherByProject = threadSearchQuery
    ? threadOtherByProject.map(([path, sessions]) => [path, sessions.filter(s => matchSession(s, threadSessionTabMap))]).filter(([, sessions]) => sessions.length > 0)
    : threadOtherByProject;

  // Flat list for keyboard nav: [new] + currentProject + all others flattened
  $: threadFlatList = [...threadFilteredCurrentProject, ...threadFilteredOtherByProject.flatMap(([, sessions]) => sessions)];

  function handleThreadPopupKeydown(event) {
    if (!threadPopupOpen) return;
    const totalItems = 1 + threadFlatList.length;
    if (event.key === 'ArrowDown') { event.preventDefault(); threadPopupSelectedIndex = (threadPopupSelectedIndex + 1) % totalItems; }
    if (event.key === 'ArrowUp') { event.preventDefault(); threadPopupSelectedIndex = (threadPopupSelectedIndex - 1 + totalItems) % totalItems; }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (threadSearchQuery && threadFlatList.length === 0) return;
      if (threadPopupSelectedIndex === 0) {
        selectSession('', '');
      } else {
        const s = threadFlatList[threadPopupSelectedIndex - 1];
        selectSession(s.sessionId, s.projectPath);
      }
    }
    if (event.key === 'Escape') { event.preventDefault(); threadPopupOpen = false; }
  }

  function handlePopupKeydown(event) {
    handleModelPopupKeydown(event);
    handleThreadPopupKeydown(event);
  }

  function formatTimeAgo(isoString) {
    if (!isoString) return '';
    const diff = Date.now() - new Date(isoString).getTime();
    const minutes = Math.floor(diff / 60000);
    if (minutes < 1) return '방금';
    if (minutes < 60) return `${minutes}분 전`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}시간 전`;
    const days = Math.floor(hours / 24);
    if (days < 30) return `${days}일 전`;
    return `${Math.floor(days / 30)}개월 전`;
  }

  // Plan viewer state
  let planViewerContent = null;

  async function viewPlanFile(tabId) {
    try {
      const content = await GetPlanContent(tabId);
      if (!content) {
        planViewerContent = null;
        return;
      }
      planViewerContent = content;
    } catch (error) {
      console.error('Plan file load error:', error);
      planViewerContent = null;
    }
  }

  function closePlanViewer() {
    planViewerContent = null;
  }

  async function refreshPlanViewer(tabId) {
    await viewPlanFile(tabId);
  }

</script>

<svelte:window on:mousemove={(e) => { handleResizeMove(e); handleEditorResizeMove(e); }} on:mouseup={() => { stopResize(); stopEditorResize(); }} on:keydown={handlePopupKeydown} on:click={(e) => { if (presetDropdownOpen && !e.target.closest('.preset-dropdown-wrapper')) presetDropdownOpen = false; }} />

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
    class:teams-mode={tab.teamsMode}
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
      <div class="panel-header-row0">
        {#if editingTabName}
          <input
            bind:this={tabNameInputEl}
            bind:value={tabNameInput}
            on:keydown={handleTabNameKeydown}
            on:blur={commitTabName}
            class="panel-title-input"
            spellcheck="false"
          />
        {:else}
          <span class="panel-title" on:dblclick|stopPropagation={startEditTabName}>{tab.name}</span>
        {/if}
        {#if $agentStore.tabRenames[tab.id]}
          <span class="agent-rename-suggestion" on:click|stopPropagation={acceptRename} title="클릭하여 적용">
            &rarr; {$agentStore.tabRenames[tab.id]}
          </span>
          <button class="agent-dismiss-btn" on:click|stopPropagation={dismissRename} title="무시">✕</button>
        {/if}
        <div class="header-row-spacer"></div>
        <button
          class="close-btn"
          on:click={() => handleCloseTab(tab.id)}
          title="탭 닫기"
        >
          ✕
        </button>
      </div>
      <div class="panel-header-row1">
        {#if getTeamsParentName(tab.id)}
          <span class="teams-worker-badge">🔗 {getTeamsParentName(tab.id)}</span>
        {/if}
        {#if $modelStore}
          <span class="header-label" title="Claude API 호출에 사용할 AI 모델">모델</span>
          <div class="model-badge-wrapper" draggable="false" on:mousedown|stopPropagation on:dragstart|preventDefault|stopPropagation>
            <button class="model-badge" on:click|stopPropagation={openModelPopup} title="클릭하여 모델 변경. Sonnet은 빠르고 경제적, Opus는 정밀하고 심층적, Haiku는 가볍고 즉각적">{$modelStore}</button>
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
        <span class="header-label" title="대화 기록이 저장되는 Claude CLI 세션">세션</span>
        <div class="thread-badge-wrapper" draggable="false" on:mousedown|stopPropagation on:dragstart|preventDefault|stopPropagation>
          <button class="thread-badge" class:has-session={!!threadPopupCurrentSessionId} style="--tab-color: {tabColor}" on:click|stopPropagation={openThreadPopup} title="클릭하여 세션 전환. 세션별로 대화 기록이 독립적으로 유지됩니다">
            {#if threadPopupCurrentSessionId}
              {threadPopupCurrentSessionId.slice(0, 8)}...
            {:else}
              새 세션
            {/if}
          </button>
        {#if threadPopupOpen}
          <div class="thread-popup">
            {#if threadDeleteConfirm}
              <div class="thread-delete-confirm">
                <div class="thread-delete-msg">이 세션을 삭제할까요?</div>
                <div class="thread-delete-preview">{threadDeleteConfirm.preview || '(비어있음)'}</div>
                <div class="thread-delete-actions">
                  <button class="thread-delete-cancel" on:click|stopPropagation={cancelDeleteSession}>취소</button>
                  <button class="thread-delete-ok" on:click|stopPropagation={executeDeleteSession}>삭제</button>
                </div>
              </div>
            {:else}
              <div class="thread-popup-header">
                <span class="thread-popup-title">세션</span>
                <button class="thread-refresh-btn" class:spinning={threadLoading} on:click|stopPropagation={refreshSessions} title="새로고침">↻</button>
              </div>
              <div class="thread-popup-divider"></div>
              <input
                class="thread-search-input"
                type="text"
                placeholder="세션 검색..."
                bind:value={threadSearchQuery}
                on:keydown|stopPropagation={handleThreadPopupKeydown}
              />
              <div
                class="thread-popup-item new-session"
                class:selected={threadPopupSelectedIndex === 0}
                on:click|stopPropagation={() => selectSession('', '')}
              >
                <span class="thread-new-icon">+</span>
                <span class="thread-new-label">새 세션</span>
                {#if !threadPopupCurrentSessionId}<span class="thread-popup-current">현재</span>{/if}
              </div>
              {#if threadFilteredCurrentProject.length > 0}
                <div class="thread-popup-divider"></div>
                <div class="thread-popup-group-header" on:click|stopPropagation={() => toggleGroupCollapse(tab.workDir)}>
                  <span class="thread-group-arrow" class:collapsed={threadCollapsedGroups[tab.workDir]}></span>
                  <span class="thread-popup-group-label">{shortenPath(tab.workDir)}</span>
                  <span class="thread-group-count">{threadFilteredCurrentProject.length}</span>
                </div>
                {#each threadFilteredCurrentProject as session, i}
                  {#if isSessionVisible(session, tab.workDir, threadCollapsedGroups[tab.workDir])}
                    <div
                      class="thread-popup-item"
                      class:selected={threadPopupSelectedIndex === i + 1}
                      on:click|stopPropagation={() => selectSession(session.sessionId, session.projectPath)}
                    >
                      <div class="thread-item-row">
                        <div class="thread-item-preview">{session.preview || '(비어있음)'}</div>
                        <button class="thread-item-cli" on:click|stopPropagation={() => resumeSessionInTerminal(session.sessionId, session.projectPath)} title="터미널에서 이어서">▶</button>
                        <button class="thread-item-delete" on:click|stopPropagation={() => confirmDeleteSession(session.sessionId, session.projectPath, session.preview)} title="삭제">✕</button>
                      </div>
                      <div class="thread-item-meta">
                        {session.messageCount}개 메시지 · {formatTimeAgo(session.lastActivity)}
                        {#if session.sessionId === threadPopupCurrentSessionId}<span class="thread-popup-current" style="color: {tabColor}">현재</span>
                        {:else if threadSessionTabMap[session.sessionId]}<span class="thread-popup-in-use" style="color: {threadSessionColorMap[session.sessionId] || '#f59e0b'}">{threadSessionTabMap[session.sessionId]}</span>
                        {/if}
                      </div>
                    </div>
                  {/if}
                {/each}
              {/if}
              {#each threadFilteredOtherByProject as [projectPath, sessions]}
                <div class="thread-popup-divider"></div>
                <div class="thread-popup-group-header" on:click|stopPropagation={() => toggleGroupCollapse(projectPath)}>
                  <span class="thread-group-arrow" class:collapsed={threadCollapsedGroups[projectPath]}></span>
                  <span class="thread-popup-group-label">{shortenPath(projectPath)}</span>
                  <span class="thread-group-count">{sessions.length}</span>
                </div>
                {#each sessions as session}
                  {#if isSessionVisible(session, projectPath, threadCollapsedGroups[projectPath])}
                    {@const flatIdx = threadFlatList.indexOf(session) + 1}
                    <div
                      class="thread-popup-item"
                      class:selected={threadPopupSelectedIndex === flatIdx}
                      on:click|stopPropagation={() => selectSession(session.sessionId, session.projectPath)}
                    >
                      <div class="thread-item-row">
                        <div class="thread-item-preview">{session.preview || '(비어있음)'}</div>
                        <button class="thread-item-cli" on:click|stopPropagation={() => resumeSessionInTerminal(session.sessionId, session.projectPath)} title="터미널에서 이어서">▶</button>
                        <button class="thread-item-delete" on:click|stopPropagation={() => confirmDeleteSession(session.sessionId, session.projectPath, session.preview)} title="삭제">✕</button>
                      </div>
                      <div class="thread-item-meta">
                        {session.messageCount}개 메시지 · {formatTimeAgo(session.lastActivity)}
                        {#if session.sessionId === threadPopupCurrentSessionId}<span class="thread-popup-current" style="color: {tabColor}">현재</span>
                        {:else if threadSessionTabMap[session.sessionId]}<span class="thread-popup-in-use" style="color: {threadSessionColorMap[session.sessionId] || '#f59e0b'}">{threadSessionTabMap[session.sessionId]}</span>
                        {/if}
                      </div>
                    </div>
                  {/if}
                {/each}
              {/each}
            {/if}
          </div>
        {/if}
        </div>
        <span class="header-label" title="Claude에게 추가로 제공할 참조 파일 목록">컨텍스트</span>
        <button
          class="context-toggle"
          on:click|stopPropagation={toggleContext}
          title="클릭하여 컨텍스트 파일 관리. 추가된 파일은 매 요청마다 Claude에게 함께 전달됩니다"
        >
          CTX {(autoContextFiles?.length || 0) + (tab.contextFiles?.length || 0)}
        </button>
        <button
          class="context-toggle plan-toggle"
          on:click|stopPropagation={() => viewPlanFile(tab.id)}
          title="현재 세션의 플랜 파일 보기"
        >
          Plan
        </button>
      </div>
      <div class="panel-header-row2">
        <span class="header-label" title="Claude CLI가 실행되는 작업 디렉토리">경로</span>
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
            <button class="workdir-display" on:click={startEditWorkDir} title="클릭하여 작업 디렉토리 변경. Claude가 파일을 읽고 쓰는 기준 경로입니다">
              {shortenPath(tab.workDir)}
            </button>
          {/if}
        </div>
      </div>
      {#if $agentStore.projectSummaries[tab.workDir]}
        <div class="agent-project-summary">
          {$agentStore.projectSummaries[tab.workDir].summary}
          {#if $agentStore.projectSummaries[tab.workDir].language}
            <span class="agent-badge">{$agentStore.projectSummaries[tab.workDir].language}</span>
          {/if}
          {#if $agentStore.projectSummaries[tab.workDir].framework && $agentStore.projectSummaries[tab.workDir].framework !== 'none'}
            <span class="agent-badge">{$agentStore.projectSummaries[tab.workDir].framework}</span>
          {/if}
        </div>
      {/if}
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

    {#if tab.teamsMode && tab.teamsState}
      <div class="worker-connections teams">
        <span class="worker-connections-label"><span class="teams-badge">Teams</span> 에이전트:</span>

        <!-- Preset controls -->
        <div class="teams-preset-controls">
          {#if showPresetNameInput}
            <input
              bind:this={presetNameInputEl}
              bind:value={presetNameInput}
              on:keydown={handlePresetNameKeydown}
              on:blur={commitSavePreset}
              class="preset-name-input"
              spellcheck="false"
              placeholder="프리셋 이름 입력 후 Enter"
            />
          {/if}
          <div class="preset-dropdown-wrapper">
            <button class="preset-dropdown-toggle" on:click|stopPropagation={() => presetDropdownOpen = !presetDropdownOpen}>
              {selectedPresetId ? (teamPresets.find(p => p.id === selectedPresetId)?.name || '프리셋 선택') : '프리셋 선택'}
              <span class="preset-dropdown-arrow">{presetDropdownOpen ? '▲' : '▼'}</span>
            </button>
            {#if presetDropdownOpen}
              <div class="preset-dropdown-panel" on:click|stopPropagation>
                {#each teamPresets as preset}
                  <div class="preset-dropdown-item" class:selected={selectedPresetId === preset.id}>
                    {#if editingPresetId === preset.id}
                      <input
                        bind:this={editingPresetInputEl}
                        bind:value={editingPresetName}
                        on:keydown={handleRenamePresetKeydown}
                        on:blur={commitRenamePreset}
                        class="preset-dropdown-input"
                        spellcheck="false"
                      />
                    {:else}
                      <span class="preset-dropdown-name" on:click={() => { onPresetSelected(preset.id); presetDropdownOpen = false; }} on:dblclick|stopPropagation={() => startRenamePreset(preset.id)} title="클릭: 적용 / 더블클릭: 이름 변경">
                        {preset.name}
                      </span>
                      <button class="preset-dropdown-delete" on:click|stopPropagation={() => deletePreset(preset.id)} title="삭제">&times;</button>
                    {/if}
                  </div>
                {/each}
                {#if teamPresets.length === 0}
                  <div class="preset-dropdown-empty">저장된 프리셋 없음</div>
                {/if}
              </div>
            {/if}
          </div>
          <button class="preset-btn" on:click|stopPropagation={startSavePreset} title="현재 구성 저장">💾</button>
        </div>

        <!-- Worker chips: toggleable for manual connect/disconnect -->
        {#each tabs.filter(t => !t.teamsMode && t.id !== tab.id) as wTab}
          <button
            class="worker-chip teams"
            class:connected={isTeamsConnected(wTab.id)}
            on:click|stopPropagation={() => toggleTeamsWorkerConnection(wTab.id)}
            title={isTeamsConnected(wTab.id) ? '연결 해제' : '연결'}
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
                  <span class="context-scope-badge" class:global={af.scope === 'global'} title={af.scope === 'global' ? 'Global: ~/.claude 에서 로드 (모든 프로젝트 공통)' : 'Project: 현재 프로젝트 디렉토리에서 로드'}>{af.scope === 'global' ? 'G' : 'P'}</span>
                  <span class="context-file-name" on:click={() => openFileInEditor(af.path)}>
                    {af.name}
                  </span>
                  <span class="context-file-path">{getContextBasePath(af.path, af.name)}</span>
                  <button class="context-delete-btn" on:click={() => deleteFileFromDisk(af.path)} title="파일 삭제">🗑</button>
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
                <span class="context-file-name" on:click={() => openFileInEditor(cf)}>
                  {shortenPath(cf)}
                </span>
                <button class="context-delete-btn" on:click={() => deleteFileFromDisk(cf)} title="파일 삭제">🗑</button>
                <button class="context-remove-btn" on:click={() => removeContextFile(cf)} title="목록에서 제거">✕</button>
              </div>
            {/each}
          </div>
          {#if !autoContextFiles.some(f => f.name === 'CLAUDE.md' && f.scope === 'project')}
              <button class="context-add-btn claude-md-btn" on:click={createClaudeMd}>+ CLAUDE.md 생성</button>
          {/if}
          <button class="context-add-btn claude-md-btn" on:click={createTabClaudeMd}>+ 탭 전용 CLAUDE.md</button>
          <button class="context-add-btn" on:click={addContextFile}>+ 파일 추가</button>
        </div>

      </div>
    {/if}

    {#if planViewerContent}
      <div class="plan-viewer-panel">
        <div class="plan-viewer-panel-header">
          <span class="plan-viewer-panel-title">Plan File</span>
          <button class="plan-viewer-panel-btn" on:click={() => refreshPlanViewer(tab.id)}>↻</button>
          <button class="plan-viewer-panel-btn" on:click={closePlanViewer}>✕</button>
        </div>
        <div class="plan-viewer-panel-content">
          <MarkdownViewer content={planViewerContent} />
        </div>
      </div>
    {/if}

    {#if $agentStore.codeReviews[tab.id]}
      <div class="code-review-panel">
        <div class="context-section-label">
          코드 리뷰
          <button class="agent-dismiss-btn small" on:click={dismissCodeReview} title="닫기">✕</button>
        </div>
        {#if $agentStore.codeReviews[tab.id].summary}
          <div class="code-review-summary">{$agentStore.codeReviews[tab.id].summary}</div>
        {/if}
        {#if $agentStore.codeReviews[tab.id].issues?.length > 0}
          <div class="code-review-issues">
            {#each $agentStore.codeReviews[tab.id].issues as issue}
              <div class="code-review-issue {issue.severity}">
                <span class="issue-severity">{issue.severity === 'error' ? '🔴' : issue.severity === 'warning' ? '🟡' : '🔵'}</span>
                <div class="issue-content">
                  <div class="issue-location">{issue.file}{issue.line ? `:${issue.line}` : ''}</div>
                  <div class="issue-message">{issue.message}</div>
                  {#if issue.suggestion}
                    <div class="issue-suggestion">💡 {issue.suggestion}</div>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="code-review-clean">문제가 발견되지 않았습니다.</div>
        {/if}
      </div>
    {/if}

    <div class="panel-content" style="flex-direction: {editorFile ? 'row' : 'column'};">
      <div class="editor-pane" style="flex: {editorSplitRatio[0]};">
        {#key tab.id}
          <ConversationTab
            tabId={tab.id}
            tabName={tab.name}
            messages={tab.messages}
            adminMode={tab.adminMode}
            teamsMode={tab.teamsMode}
            planMode={tab.planMode}
            workDir={tab.workDir}
            isTeamsWorkerBusy={isTeamsWorkerBusy(tab.id)}
            workerTabNames={Object.fromEntries(tabs.map(t => [t.id, t.name]))}
            on:refresh={() => {
              console.log('[SplitPane] Received refresh event, dispatching update');
              dispatch('update');
            }}
          />
        {/key}
      </div>

      {#if editorFile}
        <div class="editor-splitter" on:mousedown={startEditorResize}></div>
        <div class="editor-pane" style="flex: {editorSplitRatio[1]};">
          <div class="editor-header">
            {#if editorRenaming}
              <input
                bind:this={editorRenameInputEl}
                bind:value={editorRenameInput}
                on:keydown={handleEditorRenameKeydown}
                on:blur={commitEditorRename}
                class="editor-rename-input"
                spellcheck="false"
              />
            {:else}
              <span class="editor-file-path" title="클릭하여 파일명 변경" on:click={startEditorRename}>
                {shortenPath(editorFile.path)}
                {#if editorDirty}<span class="editor-dirty-indicator">*</span>{/if}
              </span>
            {/if}
            <div class="editor-header-actions">
              <button class="editor-save-btn" on:click={saveEditorFile} disabled={!editorDirty || editorSaving}>
                {editorSaving ? '저장 중...' : '저장'}
              </button>
              {#if editorCloseConfirm}
                <span class="editor-close-confirm">
                  <span>저장 안 함?</span>
                  <button class="editor-confirm-yes" on:click={closeEditor}>닫기</button>
                  <button class="editor-confirm-no" on:click={cancelCloseEditor}>취소</button>
                </span>
              {:else}
                <button class="editor-close-btn" on:click={closeEditor}>✕</button>
              {/if}
            </div>
          </div>
          <div class="editor-body">
            <div class="editor-line-numbers" bind:this={editorLineNumbersEl}><pre>{editorLineNumbers}</pre></div>
            <textarea
              class="editor-textarea"
              bind:this={editorTextarea}
              value={editorFile.content}
              on:input={handleEditorInput}
              on:keydown={handleEditorKeydown}
              on:scroll={handleEditorScroll}
              spellcheck="false"
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
            ></textarea>
          </div>
        </div>
      {/if}
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

  .panel.teams-mode {
    border-color: #0ea5e9;
  }

  .panel.plan-mode {
    border-color: #7c3aed;
  }

  .panel.plan-mode .panel-header {
    background-color: rgba(124, 58, 237, 0.08);
  }

  .panel-header {
    display: flex;
    flex-direction: column;
    padding: 4px 12px;
    background-color: var(--bg-header);
    border-bottom: 1px solid var(--border-primary);
    color: var(--text-primary);
    font-size: 13px;
    flex-shrink: 0;
    cursor: grab;
    position: relative;
  }

  .panel-header:active {
    cursor: grabbing;
  }

  .panel-header-row0 {
    display: flex;
    align-items: center;
    gap: 4px;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border-primary);
  }

  .panel-header-row1 {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-top: 4px;
  }

  .header-row-spacer {
    flex: 1;
  }

  .panel-header-row2 {
    display: flex;
    align-items: center;
    margin-top: 2px;
  }

  .header-label {
    font-size: 11px;
    font-weight: 700;
    color: var(--text-primary);
    letter-spacing: 0.3px;
    flex-shrink: 0;
    user-select: none;
    margin-left: 10px;
  }

  .header-label:first-child {
    margin-left: 0;
  }

  .panel.admin-mode .panel-header {
    background-color: #fff3e0;
  }

  .panel.teams-mode .panel-header {
    background-color: rgba(14, 165, 233, 0.08);
  }

  .panel-title {
    font-weight: 500;
    user-select: none;
    flex-shrink: 0;
    cursor: default;
  }

  .panel-title-input {
    font-weight: 500;
    font-size: inherit;
    font-family: inherit;
    background: var(--bg-secondary, #1e1e2e);
    color: var(--text-primary, #cdd6f4);
    border: 1px solid var(--accent, #89b4fa);
    border-radius: 3px;
    padding: 0 4px;
    outline: none;
    flex-shrink: 0;
    width: 120px;
  }

  .model-badge-wrapper {
    position: relative;
    flex-shrink: 0;
    cursor: default;
  }

  .model-badge {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
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

  /* Thread (session) badge & popup */
  .thread-badge-wrapper {
    position: relative;
    flex-shrink: 0;
    cursor: default;
  }

  .thread-badge {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: #10b981;
    background-color: rgba(16, 185, 129, 0.1);
    padding: 2px 8px;
    border-radius: 10px;
    border: 1px solid rgba(16, 185, 129, 0.25);
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex-shrink: 0;
    user-select: none;
    cursor: pointer;
    transition: all 0.15s;
  }

  .thread-badge:hover {
    border-color: #10b981;
    background-color: rgba(16, 185, 129, 0.18);
  }

  .thread-badge.has-session {
    color: var(--tab-color, #60a5fa);
    background-color: color-mix(in srgb, var(--tab-color, #60a5fa) 12%, transparent);
    border-color: color-mix(in srgb, var(--tab-color, #60a5fa) 25%, transparent);
  }

  .thread-badge.has-session:hover {
    border-color: var(--tab-color, #60a5fa);
    background-color: color-mix(in srgb, var(--tab-color, #60a5fa) 20%, transparent);
  }

  .thread-popup {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 200;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    box-shadow: var(--shadow-md, 0 4px 12px rgba(0,0,0,0.3));
    min-width: 260px;
    width: calc(100vw * 0.22);
    max-width: 400px;
    max-height: 420px;
    overflow-y: auto;
    margin-top: 2px;
  }

  .thread-popup-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 5px 8px;
    flex-shrink: 0;
  }

  .thread-popup-title {
    font-size: 10px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .thread-refresh-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 14px;
    cursor: pointer;
    padding: 0 2px;
    line-height: 1;
    border-radius: 3px;
    transition: color 0.15s;
  }

  .thread-refresh-btn:hover {
    color: var(--text-primary);
  }

  .thread-refresh-btn.spinning {
    animation: thread-spin 0.6s linear infinite;
  }

  @keyframes thread-spin {
    from { transform: rotate(0deg); }
    to { transform: rotate(360deg); }
  }

  .thread-popup-item {
    padding: 5px 8px;
    cursor: pointer;
    color: var(--text-primary);
    transition: background-color 0.1s;
    text-align: left;
  }

  .thread-popup-item:hover {
    background-color: var(--bg-hover);
  }

  .thread-popup-item.selected {
    background-color: rgba(16, 185, 129, 0.15);
  }

  .thread-popup-item.new-session {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 500;
  }

  .thread-new-icon {
    font-size: 12px;
    font-weight: 700;
    color: #10b981;
  }

  .thread-new-label {
    color: #10b981;
  }

  .thread-popup-divider {
    height: 1px;
    background-color: var(--border-primary);
    margin: 0;
  }

  .thread-search-input {
    width: 100%;
    padding: 4px 8px;
    border: 1px solid var(--border-primary, #333);
    border-radius: 4px;
    background: var(--bg-secondary, #1e1e1e);
    color: var(--text-primary, #e0e0e0);
    font-size: 12px;
    outline: none;
    box-sizing: border-box;
  }
  .thread-search-input:focus {
    border-color: var(--accent, #6366f1);
  }

  .thread-item-row {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .thread-item-preview {
    font-size: 12px;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
    min-width: 0;
    line-height: 1.4;
  }

  .thread-item-cli {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 10px;
    cursor: pointer;
    padding: 0 3px;
    border-radius: 2px;
    opacity: 0;
    flex-shrink: 0;
    transition: opacity 0.15s, color 0.15s;
    line-height: 1;
  }

  .thread-popup-item:hover .thread-item-cli {
    opacity: 0.6;
  }

  .thread-item-cli:hover {
    opacity: 1 !important;
    color: #10b981;
  }

  .thread-item-delete {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 10px;
    cursor: pointer;
    padding: 0 3px;
    border-radius: 3px;
    opacity: 0;
    flex-shrink: 0;
    transition: all 0.15s;
    line-height: 1;
  }

  .thread-popup-item:hover .thread-item-delete {
    opacity: 0.6;
  }

  .thread-item-delete:hover {
    opacity: 1 !important;
    color: var(--error, #ef4444);
    background-color: rgba(239, 68, 68, 0.1);
  }

  .thread-item-meta {
    font-size: 10px;
    color: var(--text-muted);
    margin-top: 1px;
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .thread-popup-current {
    font-size: 9px;
    font-weight: 600;
    margin-left: auto;
    flex-shrink: 0;
  }

  .thread-popup-in-use {
    font-size: 9px;
    font-weight: 600;
    margin-left: auto;
    flex-shrink: 0;
  }

  .thread-popup-group-header {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 6px 8px 4px;
    cursor: pointer;
    user-select: none;
    transition: background-color 0.1s;
  }

  .thread-popup-group-header:hover {
    background-color: var(--bg-hover);
  }

  .thread-group-arrow {
    flex-shrink: 0;
    width: 0;
    height: 0;
    border-left: 4px solid transparent;
    border-right: 4px solid transparent;
    border-top: 5px solid var(--text-muted);
    transition: transform 0.15s;
  }

  .thread-group-arrow.collapsed {
    transform: rotate(-90deg);
  }

  .thread-popup-group-label {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    font-weight: 700;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }

  .thread-group-count {
    font-size: 9px;
    font-weight: 600;
    color: var(--text-muted);
    background-color: var(--bg-hover, rgba(255,255,255,0.06));
    padding: 1px 5px;
    border-radius: 8px;
    flex-shrink: 0;
    margin-left: auto;
  }

  .thread-item-project {
    color: var(--text-muted);
    opacity: 0.7;
    max-width: 100px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: inline-block;
    vertical-align: middle;
  }

  /* Delete confirmation overlay inside popup */
  .thread-delete-confirm {
    padding: 12px;
    text-align: center;
  }

  .thread-delete-msg {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    margin-bottom: 6px;
  }

  .thread-delete-preview {
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    margin-bottom: 10px;
    padding: 0 8px;
  }

  .thread-delete-actions {
    display: flex;
    gap: 8px;
    justify-content: center;
  }

  .thread-delete-cancel,
  .thread-delete-ok {
    padding: 4px 14px;
    border-radius: 4px;
    font-size: 11px;
    font-weight: 500;
    cursor: pointer;
    border: 1px solid var(--border-primary);
    transition: all 0.15s;
  }

  .thread-delete-cancel {
    background: var(--bg-secondary);
    color: var(--text-primary);
  }

  .thread-delete-cancel:hover {
    background: var(--bg-hover);
  }

  .thread-delete-ok {
    background: var(--error, #ef4444);
    color: #fff;
    border-color: var(--error, #ef4444);
  }

  .thread-delete-ok:hover {
    opacity: 0.85;
  }

  .workdir-area {
    flex: 1;
    min-width: 0;
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
    font-size: 11px;
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
    text-align: left;
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

  .context-file-path {
    flex-shrink: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 10px;
    color: var(--text-secondary);
    opacity: 0.8;
    max-width: 50%;
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
    text-align: left;
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

  .context-delete-btn {
    background: none;
    border: none;
    cursor: pointer;
    padding: 1px 2px;
    font-size: 11px;
    border-radius: 3px;
    flex-shrink: 0;
    opacity: 0;
    transition: all 0.15s;
    line-height: 1;
  }

  .context-file-item:hover .context-delete-btn {
    opacity: 0.5;
  }

  .context-delete-btn:hover {
    opacity: 1 !important;
    background-color: rgba(239, 68, 68, 0.1);
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
    text-align: left;
  }

  .context-add-btn:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
    border-color: var(--accent);
  }

  .context-add-btn.claude-md-btn {
    border-style: solid;
    border-color: var(--accent, #6366f1);
    color: var(--accent, #6366f1);
  }

  .context-add-btn.claude-md-btn:hover {
    background-color: var(--accent, #6366f1);
    color: white;
    border-color: var(--accent, #6366f1);
  }

  /* Agent UI styles */
  .agent-rename-suggestion {
    font-size: 12px;
    color: #10b981;
    cursor: pointer;
    padding: 1px 6px;
    border-radius: 3px;
    transition: background-color 0.15s;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 150px;
  }

  .agent-rename-suggestion:hover {
    background-color: rgba(16, 185, 129, 0.15);
  }

  .agent-dismiss-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 11px;
    padding: 0 3px;
    border-radius: 2px;
    line-height: 1;
    transition: color 0.15s;
  }

  .agent-dismiss-btn:hover {
    color: var(--text-primary);
  }

  .agent-dismiss-btn.small {
    font-size: 10px;
    margin-left: 4px;
  }

  .agent-project-summary {
    font-size: 11px;
    color: var(--text-muted);
    padding: 2px 12px 4px;
    line-height: 1.4;
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .agent-badge {
    font-size: 10px;
    padding: 1px 5px;
    border-radius: 3px;
    background-color: rgba(16, 185, 129, 0.12);
    color: #10b981;
    white-space: nowrap;
  }

  /* Editor panel */
  .editor-pane {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    min-width: 0;
  }

  .editor-splitter {
    width: 4px;
    cursor: col-resize;
    background-color: var(--border-primary);
    flex-shrink: 0;
    transition: background-color 0.15s;
  }

  .editor-splitter:hover {
    background-color: var(--accent, #6366f1);
  }

  .editor-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 8px;
    background-color: var(--bg-secondary);
    border-bottom: 1px solid var(--border-primary);
    font-size: 11px;
    flex-shrink: 0;
    gap: 8px;
  }

  .editor-file-path {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
    cursor: pointer;
    padding: 1px 4px;
    border-radius: 3px;
    transition: background-color 0.15s, color 0.15s;
  }

  .editor-file-path:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
  }

  .editor-rename-input {
    flex: 1;
    min-width: 0;
    background-color: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--accent);
    border-radius: 3px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    padding: 1px 4px;
    outline: none;
    box-sizing: border-box;
  }

  .editor-dirty-indicator {
    color: var(--accent, #6366f1);
    font-weight: bold;
    margin-left: 2px;
  }

  .editor-header-actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
  }

  .editor-save-btn {
    background-color: var(--accent, #6366f1);
    color: white;
    border: none;
    border-radius: 3px;
    padding: 2px 8px;
    font-size: 11px;
    cursor: pointer;
    transition: opacity 0.15s;
  }

  .editor-save-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .editor-save-btn:not(:disabled):hover {
    opacity: 0.85;
  }

  .editor-close-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 13px;
    padding: 2px 6px;
    border-radius: 3px;
    line-height: 1;
  }

  .editor-close-btn:hover {
    background-color: var(--bg-hover);
    color: var(--error, #ef4444);
  }

  .editor-close-confirm {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-muted);
  }

  .editor-confirm-yes, .editor-confirm-no {
    background: none;
    border: 1px solid var(--border-color, #555);
    border-radius: 4px;
    padding: 1px 8px;
    font-size: 11px;
    cursor: pointer;
    line-height: 1.4;
  }

  .editor-confirm-yes {
    color: var(--error, #ef4444);
    border-color: var(--error, #ef4444);
  }

  .editor-confirm-yes:hover {
    background-color: var(--error, #ef4444);
    color: white;
  }

  .editor-confirm-no {
    color: var(--text-muted);
  }

  .editor-confirm-no:hover {
    background-color: var(--bg-hover);
  }

  .editor-body {
    display: flex;
    flex-direction: row;
    flex: 1;
    overflow: hidden;
  }

  .editor-line-numbers {
    width: 40px;
    flex-shrink: 0;
    overflow: hidden;
    background-color: var(--bg-secondary, #1e1e3a);
    border-right: 1px solid var(--border-primary);
    user-select: none;
  }

  .editor-line-numbers pre {
    margin: 0;
    padding: 8px 4px 8px 0;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    line-height: 1.5;
    color: var(--text-muted);
    text-align: right;
    white-space: pre;
  }

  .editor-textarea {
    flex: 1;
    resize: none;
    border: none;
    outline: none;
    padding: 8px;
    margin: 0;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    line-height: 1.5;
    tab-size: 2;
    color: var(--text-primary);
    background-color: var(--bg-primary, #1a1a2e);
    white-space: pre;
    overflow: auto;
    min-width: 0;
  }

  .editor-textarea:focus {
    outline: none;
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

  .worker-connections.teams {
    background-color: rgba(14, 165, 233, 0.05);
  }

  .worker-chip.teams.connected {
    background-color: rgba(14, 165, 233, 0.15);
    border-color: #0ea5e9;
    color: #0ea5e9;
    font-weight: 600;
  }

  .worker-chip.teams:hover {
    border-color: #0ea5e9;
    color: var(--text-primary);
  }

  .worker-chip.teams.connected:hover {
    background-color: rgba(239, 68, 68, 0.1);
    border-color: #ef4444;
    color: #ef4444;
  }

  .teams-badge {
    display: inline-block;
    padding: 1px 5px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 700;
    background-color: rgba(14, 165, 233, 0.2);
    color: #0ea5e9;
    letter-spacing: 0.3px;
    text-transform: uppercase;
  }

  .teams-worker-badge {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    padding: 1px 6px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 600;
    background-color: rgba(14, 165, 233, 0.15);
    color: #0ea5e9;
    letter-spacing: 0.3px;
    white-space: nowrap;
    margin-left: 4px;
  }

  .teams-preset-controls {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    margin-bottom: 4px;
  }

  .preset-name-input {
    flex: 1;
    padding: 2px 4px;
    font-size: 11px;
    border: 1px solid var(--accent, #89b4fa);
    border-radius: 4px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    outline: none;
    min-width: 0;
  }

  .preset-dropdown-wrapper {
    position: relative;
    flex: 1;
    min-width: 0;
  }

  .preset-dropdown-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 3px 8px;
    font-size: 11px;
    font-weight: 500;
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    cursor: pointer;
    transition: border-color 0.15s;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .preset-dropdown-toggle:hover {
    border-color: #0ea5e9;
  }

  .preset-dropdown-arrow {
    font-size: 9px;
    margin-left: 6px;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .preset-dropdown-panel {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 100;
    margin-top: 2px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.3);
    max-height: 200px;
    overflow-y: auto;
  }

  .preset-dropdown-item {
    display: flex;
    align-items: center;
    padding: 5px 8px;
    font-size: 11px;
    cursor: pointer;
    transition: background-color 0.1s;
  }

  .preset-dropdown-item:hover {
    background-color: var(--bg-hover);
  }

  .preset-dropdown-item.selected {
    background-color: rgba(14, 165, 233, 0.15);
    color: #0ea5e9;
    font-weight: 600;
  }

  .preset-dropdown-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .preset-dropdown-delete {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    margin-left: 4px;
    padding: 0;
    border: none;
    border-radius: 50%;
    background: transparent;
    color: var(--text-muted);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    opacity: 0.5;
    transition: all 0.15s;
    flex-shrink: 0;
  }

  .preset-dropdown-delete:hover {
    opacity: 1;
    background: rgba(239, 68, 68, 0.2);
    color: #ef4444;
  }

  .preset-dropdown-input {
    flex: 1;
    padding: 2px 4px;
    font-size: 11px;
    border: none;
    border-bottom: 1px solid var(--accent, #89b4fa);
    background: transparent;
    color: var(--text-primary);
    outline: none;
    min-width: 0;
  }

  .preset-dropdown-empty {
    padding: 8px;
    font-size: 11px;
    color: var(--text-muted);
    text-align: center;
  }

  .preset-btn {
    padding: 2px 6px;
    font-size: 12px;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    background: var(--bg-secondary);
    cursor: pointer;
    line-height: 1;
    flex-shrink: 0;
  }

  .preset-btn:hover {
    background: var(--bg-tertiary, rgba(255,255,255,0.1));
    border-color: #0ea5e9;
  }

  .code-review-panel {
    padding: 8px;
    border-top: 1px solid var(--border-color);
  }
  .code-review-summary {
    font-size: 12px;
    color: var(--text-secondary);
    margin-bottom: 6px;
    padding: 4px 8px;
    background: var(--bg-secondary);
    border-radius: 4px;
  }
  .code-review-issue {
    display: flex;
    gap: 6px;
    padding: 6px 8px;
    border-radius: 4px;
    margin-bottom: 4px;
    font-size: 12px;
  }
  .code-review-issue.error { background: rgba(239, 68, 68, 0.08); }
  .code-review-issue.warning { background: rgba(234, 179, 8, 0.08); }
  .code-review-issue.info { background: rgba(59, 130, 246, 0.08); }
  .issue-severity { flex-shrink: 0; }
  .issue-location { font-family: monospace; font-size: 11px; color: var(--text-muted); }
  .issue-message { margin-top: 2px; }
  .issue-suggestion { margin-top: 4px; color: #10b981; font-size: 11px; }
  .code-review-clean { font-size: 12px; color: #10b981; padding: 8px; text-align: center; }

  /* Plan viewer panel */
  .plan-toggle {
    margin-left: 4px;
    opacity: 0.7;
  }
  .plan-toggle:hover {
    opacity: 1;
  }
  .plan-viewer-panel {
    border-top: 1px solid var(--border-primary);
    padding: 8px 12px;
    max-height: 300px;
    overflow-y: auto;
  }
  .plan-viewer-panel-header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
  }
  .plan-viewer-panel-title {
    font-weight: 600;
    font-size: 0.85em;
    flex: 1;
  }
  .plan-viewer-panel-btn {
    background: none;
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-size: 0.8em;
    padding: 2px 6px;
  }
  .plan-viewer-panel-btn:hover {
    color: var(--text-primary);
    background: var(--bg-hover);
  }
  .plan-viewer-panel-content {
    font-size: 0.85em;
  }
</style>
