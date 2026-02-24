<script>
  import { onMount } from 'svelte';
  import ManagementTab from './ManagementTab.svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs } from '../../../wailsjs/go/main/App';

  let tabs = [];
  let panels = [
    { id: 'panel-1', tabId: 'management', width: 50, height: 50 },
    { id: 'panel-2', tabId: 'conversation-1', width: 50, height: 50 },
    { id: 'panel-3', tabId: 'conversation-2', width: 50, height: 50 },
    { id: 'panel-4', tabId: 'conversation-3', width: 50, height: 50 },
    { id: 'panel-5', tabId: 'conversation-4', width: 50, height: 50 },
  ];

  let gridLayout = {
    columns: '1fr 1fr 1fr',
    rows: '1fr 1fr',
    areas: `
      "panel-1 panel-2 panel-3"
      "panel-1 panel-4 panel-5"
    `
  };

  let resizing = null;

  onMount(async () => {
    await loadTabs();
  });

  async function loadTabs() {
    try {
      tabs = await GetTabs();
    } catch (error) {
      console.error('Failed to load tabs:', error);
    }
  }

  function getTabById(tabId) {
    return tabs.find(tab => tab.id === tabId);
  }

  function startResize(panelId, direction) {
    resizing = { panelId, direction };
  }

  function handleMouseMove(event) {
    if (!resizing) return;
    // TODO: Implement resize logic
  }

  function stopResize() {
    resizing = null;
  }

  $: tabsMap = tabs.reduce((acc, tab) => {
    acc[tab.id] = tab;
    return acc;
  }, {});
</script>

<svelte:window on:mousemove={handleMouseMove} on:mouseup={stopResize} />

<div class="split-layout" style="
  display: grid;
  grid-template-columns: {gridLayout.columns};
  grid-template-rows: {gridLayout.rows};
  grid-template-areas: {gridLayout.areas};
  gap: 4px;
  height: 100vh;
  padding: 4px;
  background-color: #1e1e1e;
">
  {#each panels as panel}
    {@const tab = tabsMap[panel.tabId]}
    {#if tab}
      <div class="panel" style="grid-area: {panel.id};">
        <div class="panel-header">
          <span class="panel-title">{tab.name}</span>
          <div class="panel-actions">
            <!-- TODO: Add split/close buttons -->
          </div>
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

<style>
  .split-layout {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Roboto', sans-serif;
  }

  .panel {
    display: flex;
    flex-direction: column;
    background-color: #252526;
    border: 1px solid #3d3d3d;
    border-radius: 4px;
    overflow: hidden;
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
  }

  .panel-title {
    font-weight: 500;
  }

  .panel-actions {
    display: flex;
    gap: 4px;
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
