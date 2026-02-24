<script>
  import { onMount } from 'svelte';
  import ManagementTab from './ManagementTab.svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs, AddNewTab, RemoveTab } from '../../../wailsjs/go/main/App';

  let tabs = [];
  let panels = []; // Array of panel configs with position info
  let draggingPanel = null;
  let dragOverPanel = null;

  onMount(async () => {
    await loadTabs();
  });

  async function loadTabs() {
    try {
      tabs = await GetTabs();

      // Initialize panel layout if empty
      if (panels.length === 0 && tabs.length > 0) {
        // Default layout: grid 2x3
        const managementTab = tabs.find(t => t.id === 'management');
        const conversationTabs = tabs.filter(t => t.id !== 'management');

        panels = [
          { id: 'panel-0', tabId: managementTab?.id, row: 0, col: 0, rowSpan: 2, colSpan: 1 },
          ...conversationTabs.map((tab, idx) => ({
            id: `panel-${idx + 1}`,
            tabId: tab.id,
            row: Math.floor(idx / 2),
            col: (idx % 2) + 1,
            rowSpan: 1,
            colSpan: 1
          }))
        ];
      }
    } catch (error) {
      console.error('Failed to load tabs:', error);
    }
  }

  async function handleAddNewTab() {
    try {
      const newTab = await AddNewTab();
      tabs = [...tabs, newTab];

      // Add new panel in the next available grid position
      const newPanel = {
        id: `panel-${panels.length}`,
        tabId: newTab.id,
        row: Math.floor(panels.length / 3),
        col: panels.length % 3,
        rowSpan: 1,
        colSpan: 1
      };
      panels = [...panels, newPanel];
    } catch (error) {
      console.error('Failed to add new tab:', error);
      alert('새 탭 추가 실패');
    }
  }

  async function handleClosePanel(panelId, tabId) {
    try {
      if (tabId === 'management') {
        alert('관리 탭은 닫을 수 없습니다.');
        return;
      }

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
</script>

<div class="layout-container">
  <!-- Top toolbar -->
  <div class="toolbar">
    <button class="add-tab-btn" on:click={handleAddNewTab}>
      <span class="icon">+</span>
      새 탭
    </button>
  </div>

  <!-- Panels grid -->
  <div class="panels-grid">
    {#each panels as panel (panel.id)}
      {@const tab = getTabById(panel.tabId)}
      {#if tab}
        <div
          class="panel"
          class:dragging={draggingPanel?.id === panel.id}
          class:drag-over={dragOverPanel?.id === panel.id}
          style="
            grid-row: {panel.row + 1} / span {panel.rowSpan};
            grid-column: {panel.col + 1} / span {panel.colSpan};
          "
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
            {#if tab.id === 'management'}
              <ManagementTab />
            {:else}
              <ConversationTab
                tabId={tab.id}
                tabName={tab.name}
                messages={tab.messages}
              />
            {/if}
          </div>
        </div>
      {/if}
    {/each}
  </div>
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
    gap: 8px;
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

  .add-tab-btn:hover {
    background-color: #106ebe;
  }

  .add-tab-btn .icon {
    font-size: 16px;
    font-weight: bold;
  }

  .panels-grid {
    flex: 1;
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    grid-template-rows: repeat(2, 1fr);
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
  }

  .panel-content :global(.conversation-tab),
  .panel-content :global(.management-tab) {
    height: 100%;
  }
</style>
