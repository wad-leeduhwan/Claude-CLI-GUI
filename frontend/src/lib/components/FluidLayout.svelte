<script>
  import { onMount } from 'svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs, AddNewTab, RemoveTab, GetSettings, UpdateSettings } from '../../../wailsjs/go/main/App';

  let tabs = [];
  let panels = [];
  let draggingPanel = null;
  let dragOverPanel = null;

  onMount(async () => {
    await loadData();
  });

  async function loadData() {
    try {
      const newTabs = await GetTabs();

      if (panels.length === 0) {
        // Initial load: create panels from tabs
        panels = newTabs.map((tab, idx) => ({
          id: `panel-${idx}`,
          tabId: tab.id
        }));
      } else {
        // Update: preserve panel order but update tab references
        const existingTabIds = new Set(panels.map(p => p.tabId));
        const newTabIds = new Set(newTabs.map(t => t.id));

        // Keep panels for tabs that still exist
        const preservedPanels = panels.filter(p => newTabIds.has(p.tabId));

        // Add new panels for new tabs
        const newPanels = newTabs
          .filter(t => !existingTabIds.has(t.id))
          .map((tab, idx) => ({
            id: `panel-${panels.length + idx}`,
            tabId: tab.id
          }));

        panels = [...preservedPanels, ...newPanels];
      }

      // Force reactivity by reassigning
      tabs = newTabs;
    } catch (error) {
      console.error('Failed to load data:', error);
    }
  }

  async function handleAddNewTab() {
    if (tabs.length >= 6) {
      alert('최대 6개의 탭만 생성할 수 있습니다.');
      return;
    }

    try {
      const newTab = await AddNewTab();
      tabs = [...tabs, newTab];

      const newPanel = {
        id: `panel-${panels.length}`,
        tabId: newTab.id
      };
      panels = [...panels, newPanel];
    } catch (error) {
      console.error('Failed to add new tab:', error);
      alert('새 탭 추가 실패: 최대 6개까지만 가능합니다.');
    }
  }

  $: canAddTab = tabs.length < 6;

  async function handleClosePanel(panelId, tabId) {
    try {
      await RemoveTab(tabId);
      panels = panels.filter(p => p.id !== panelId);
      tabs = tabs.filter(t => t.id !== tabId);
    } catch (error) {
      console.error('Failed to close panel:', error);
      alert('탭 닫기 실패');
    }
  }

  function startDrag(event, panel) {
    draggingPanel = panel;
    event.dataTransfer.effectAllowed = 'move';
  }

  function dragOver(event, panel) {
    event.preventDefault();
    dragOverPanel = panel;
  }

  function drop(event, targetPanel) {
    event.preventDefault();

    if (!draggingPanel || draggingPanel.id === targetPanel.id) {
      draggingPanel = null;
      dragOverPanel = null;
      return;
    }

    // Swap positions
    const dragIdx = panels.findIndex(p => p.id === draggingPanel.id);
    const targetIdx = panels.findIndex(p => p.id === targetPanel.id);

    if (dragIdx !== -1 && targetIdx !== -1) {
      const newPanels = [...panels];

      // Swap tabIds
      const tempTabId = newPanels[dragIdx].tabId;
      newPanels[dragIdx].tabId = newPanels[targetIdx].tabId;
      newPanels[targetIdx].tabId = tempTabId;

      panels = newPanels;
    }

    draggingPanel = null;
    dragOverPanel = null;
  }

  function getTabById(tabId) {
    return tabs.find(tab => tab.id === tabId);
  }

  // Calculate optimal grid layout based on panel count
  $: gridStyle = calculateGridLayout(panels.length);

  function calculateGridLayout(count) {
    if (count === 0) return { columns: 1, rows: 1 };
    if (count === 1) return { columns: 1, rows: 1 };
    if (count === 2) return { columns: 2, rows: 1 };
    if (count === 3) return { columns: 3, rows: 1 };
    if (count === 4) return { columns: 2, rows: 2 };
    if (count === 5) return { columns: 3, rows: 2 };
    if (count === 6) return { columns: 3, rows: 2 };
    return { columns: 3, rows: 2 }; // Max 6 tabs
  }
</script>

<div class="layout-container">
  <!-- Top toolbar -->
  <div class="toolbar">
    <button class="add-tab-btn" on:click={handleAddNewTab} disabled={!canAddTab}>
      <span class="icon">+</span>
      새 탭 {tabs.length}/6
    </button>
  </div>

  <!-- Fluid panels grid -->
  <div
    class="panels-grid"
    style="
      grid-template-columns: repeat({gridStyle.columns}, 1fr);
      grid-template-rows: repeat({gridStyle.rows}, 1fr);
    "
  >
    {#each panels as panel (panel.id)}
      {@const tab = getTabById(panel.tabId)}
      {#if tab}
        <div
          class="panel"
          class:dragging={draggingPanel?.id === panel.id}
          class:drag-over={dragOverPanel?.id === panel.id}
          class:admin-mode={tab.adminMode}
          class:teams-mode={tab.teamsMode}
          draggable="true"
          on:dragstart={(e) => startDrag(e, panel)}
          on:dragover={(e) => dragOver(e, panel)}
          on:drop={(e) => drop(e, panel)}
        >
          <div class="panel-header">
            <span class="panel-title">{tab.name}</span>
            <button
              class="close-btn"
              on:click={() => handleClosePanel(panel.id, tab.id)}
              title="탭 닫기"
            >
              ✕
            </button>
          </div>
          <div class="panel-content">
            <ConversationTab
              tabId={tab.id}
              tabName={tab.name}
              messages={tab.messages}
              adminMode={tab.adminMode}
              teamsMode={tab.teamsMode}
              on:refresh={loadData}
            />
          </div>
        </div>
      {/if}
    {/each}
  </div>

  {#if panels.length === 0}
    <div class="empty-state">
      <p>+ 새 탭 버튼을 눌러 대화를 시작하세요</p>
    </div>
  {/if}
</div>

<style>
  .layout-container {
    display: flex;
    flex-direction: column;
    height: 100vh;
    background-color: #1e1e1e;
    overflow: hidden;
  }

  .toolbar {
    display: flex;
    align-items: center;
    padding: 8px 12px;
    background-color: #2d2d30;
    border-bottom: 1px solid #3d3d3d;
    gap: 12px;
  }

  .add-tab-btn {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    background-color: #0078d4;
    color: #ffffff;
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 13px;
    transition: background-color 0.2s;
  }

  .add-tab-btn:hover:not(:disabled) {
    background-color: #106ebe;
  }

  .add-tab-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .add-tab-btn .icon {
    font-size: 16px;
    font-weight: bold;
  }

  .panels-grid {
    flex: 1;
    display: grid;
    gap: 4px;
    padding: 4px;
    overflow: hidden;
  }

  .panel {
    display: flex;
    flex-direction: column;
    background-color: #252526;
    border: 1px solid #3d3d3d;
    border-radius: 4px;
    overflow: hidden;
    cursor: move;
    transition: transform 0.1s, box-shadow 0.1s;
    min-width: 0;
    min-height: 0;
  }

  .panel.admin-mode {
    border-color: #ff8c00;
  }

  .panel.dragging {
    opacity: 0.5;
    transform: scale(0.98);
  }

  .panel.drag-over {
    border-color: #0078d4;
    box-shadow: 0 0 8px rgba(0, 120, 212, 0.5);
  }

  .panel-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 8px 12px;
    background-color: #2d2d30;
    border-bottom: 1px solid #3d3d3d;
    color: #cccccc;
    font-size: 13px;
    flex-shrink: 0;
  }

  .panel.admin-mode .panel-header {
    background-color: #3d2d20;
  }

  .panel-title {
    font-weight: 500;
    user-select: none;
  }

  .close-btn {
    background: none;
    border: none;
    color: #a0a0a0;
    cursor: pointer;
    padding: 2px 6px;
    font-size: 16px;
    line-height: 1;
    border-radius: 3px;
    transition: all 0.2s;
  }

  .close-btn:hover {
    background-color: #e81123;
    color: #ffffff;
  }

  .panel-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  .panel-content :global(.conversation-tab) {
    height: 100%;
  }

  .empty-state {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: #6d6d6d;
    font-size: 16px;
  }
</style>
