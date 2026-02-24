<script>
  import { onMount } from 'svelte';
  import ManagementTab from './ManagementTab.svelte';
  import ConversationTab from './ConversationTab.svelte';
  import { GetTabs } from '../../../wailsjs/go/main/App';

  let tabs = [];

  // Layout: 2 rows, 3 columns
  // Row 1: Management (spans 2 rows) | Conv1 | Conv2
  // Row 2: Management (continued)     | Conv3 | Conv4

  let columnSizes = [30, 35, 35]; // percentages
  let rowSizes = [50, 50]; // percentages

  let resizing = null;
  let containerRef;

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

  function startResizeColumn(index, event) {
    event.preventDefault();
    resizing = {
      type: 'column',
      index,
      startX: event.clientX,
      startSizes: [...columnSizes]
    };
  }

  function startResizeRow(index, event) {
    event.preventDefault();
    resizing = {
      type: 'row',
      index,
      startY: event.clientY,
      startSizes: [...rowSizes]
    };
  }

  function handleMouseMove(event) {
    if (!resizing || !containerRef) return;

    const rect = containerRef.getBoundingClientRect();

    if (resizing.type === 'column') {
      const deltaX = event.clientX - resizing.startX;
      const deltaPercent = (deltaX / rect.width) * 100;

      const newSizes = [...resizing.startSizes];
      const index = resizing.index;

      // Adjust current and next column
      const newCurrent = resizing.startSizes[index] + deltaPercent;
      const newNext = resizing.startSizes[index + 1] - deltaPercent;

      // Enforce minimum size of 15%
      if (newCurrent >= 15 && newNext >= 15) {
        newSizes[index] = newCurrent;
        newSizes[index + 1] = newNext;
        columnSizes = newSizes;
      }
    } else if (resizing.type === 'row') {
      const deltaY = event.clientY - resizing.startY;
      const deltaPercent = (deltaY / rect.height) * 100;

      const newSizes = [...resizing.startSizes];
      const index = resizing.index;

      const newCurrent = resizing.startSizes[index] + deltaPercent;
      const newNext = resizing.startSizes[index + 1] - deltaPercent;

      if (newCurrent >= 15 && newNext >= 15) {
        newSizes[index] = newCurrent;
        newSizes[index + 1] = newNext;
        rowSizes = newSizes;
      }
    }
  }

  function stopResize() {
    resizing = null;
  }

  $: managementTab = tabs.find(tab => tab.id === 'management');
  $: conversationTabs = tabs.filter(tab => tab.id !== 'management');
</script>

<svelte:window on:mousemove={handleMouseMove} on:mouseup={stopResize} />

<div class="resizable-container" bind:this={containerRef}>
  <div class="grid-layout" style="
    grid-template-columns: {columnSizes[0]}% {columnSizes[1]}% {columnSizes[2]}%;
    grid-template-rows: {rowSizes[0]}% {rowSizes[1]}%;
  ">
    <!-- Management panel (spans 2 rows) -->
    {#if managementTab}
      <div class="panel" style="grid-column: 1; grid-row: 1 / 3;">
        <div class="panel-header">
          <span class="panel-title">{managementTab.name}</span>
        </div>
        <div class="panel-content">
          <ManagementTab />
        </div>
      </div>
    {/if}

    <!-- Conversation panels -->
    {#each conversationTabs as tab, index}
      {@const col = (index % 2) + 2}
      {@const row = Math.floor(index / 2) + 1}
      <div class="panel" style="grid-column: {col}; grid-row: {row};">
        <div class="panel-header">
          <span class="panel-title">{tab.name}</span>
        </div>
        <div class="panel-content">
          <ConversationTab
            tabId={tab.id}
            tabName={tab.name}
            messages={tab.messages}
          />
        </div>
      </div>
    {/each}

    <!-- Vertical splitters -->
    <div
      class="splitter vertical"
      style="grid-column: 1; grid-row: 1 / 3; justify-self: end;"
      on:mousedown={(e) => startResizeColumn(0, e)}
    ></div>
    <div
      class="splitter vertical"
      style="grid-column: 2; grid-row: 1 / 3; justify-self: end;"
      on:mousedown={(e) => startResizeColumn(1, e)}
    ></div>

    <!-- Horizontal splitter -->
    <div
      class="splitter horizontal"
      style="grid-column: 2 / 4; grid-row: 1; align-self: end;"
      on:mousedown={(e) => startResizeRow(0, e)}
    ></div>
  </div>
</div>

<style>
  .resizable-container {
    width: 100vw;
    height: 100vh;
    overflow: hidden;
    background-color: #1e1e1e;
  }

  .grid-layout {
    display: grid;
    gap: 0;
    width: 100%;
    height: 100%;
    position: relative;
  }

  .panel {
    display: flex;
    flex-direction: column;
    background-color: #252526;
    border: 1px solid #3d3d3d;
    overflow: hidden;
    position: relative;
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
  }

  .panel-content {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .splitter {
    background-color: #3d3d3d;
    z-index: 10;
    position: relative;
  }

  .splitter.vertical {
    width: 4px;
    cursor: col-resize;
    margin-left: -2px;
  }

  .splitter.horizontal {
    height: 4px;
    cursor: row-resize;
    margin-top: -2px;
  }

  .splitter:hover,
  .splitter:active {
    background-color: #0078d4;
  }

  .panel-content :global(.conversation-tab),
  .panel-content :global(.management-tab) {
    height: 100%;
  }
</style>
