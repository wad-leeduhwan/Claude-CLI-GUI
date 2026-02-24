<script>
  import { onMount } from 'svelte';
  import { GetTabs } from '../../../wailsjs/go/main/App';
  import ManagementTab from './ManagementTab.svelte';
  import ConversationTab from './ConversationTab.svelte';

  let tabs = [];
  let activeTabId = 'management';

  onMount(async () => {
    await loadTabs();
  });

  async function loadTabs() {
    try {
      tabs = await GetTabs();
      if (tabs.length > 0) {
        activeTabId = tabs[0].id;
      }
    } catch (error) {
      console.error('Failed to load tabs:', error);
    }
  }

  function selectTab(tabId) {
    activeTabId = tabId;
  }

  $: activeTab = tabs.find(tab => tab.id === activeTabId);
</script>

<div class="tab-container">
  <div class="tab-header">
    {#each tabs as tab}
      <button
        class="tab-button"
        class:active={tab.id === activeTabId}
        on:click={() => selectTab(tab.id)}
      >
        {tab.name}
      </button>
    {/each}
  </div>

  <div class="tab-content">
    {#if activeTab}
      {#if activeTab.id === 'management'}
        <ManagementTab />
      {:else}
        <ConversationTab tabId={activeTab.id} tabName={activeTab.name} messages={activeTab.messages} />
      {/if}
    {/if}
  </div>
</div>

<style>
  .tab-container {
    display: flex;
    flex-direction: column;
    height: 100vh;
    background-color: #1e1e1e;
    color: #e0e0e0;
  }

  .tab-header {
    display: flex;
    background-color: #2d2d2d;
    border-bottom: 1px solid #3d3d3d;
    padding: 0;
  }

  .tab-button {
    padding: 12px 24px;
    background-color: transparent;
    color: #a0a0a0;
    border: none;
    border-bottom: 2px solid transparent;
    cursor: pointer;
    font-size: 14px;
    transition: all 0.2s;
  }

  .tab-button:hover {
    background-color: #3d3d3d;
    color: #ffffff;
  }

  .tab-button.active {
    color: #ffffff;
    border-bottom-color: #0078d4;
    background-color: #1e1e1e;
  }

  .tab-content {
    flex: 1;
    overflow: hidden;
  }
</style>
