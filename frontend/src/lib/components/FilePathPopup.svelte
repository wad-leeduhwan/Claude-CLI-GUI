<script>
  import { createEventDispatcher } from 'svelte';

  export let items = [];
  export let selectedIndex = 0;

  const dispatch = createEventDispatcher();

  function getIcon(item) {
    if (item.isDir) return '\u{1F4C1}';
    const ext = item.ext?.toLowerCase() || '';
    const iconMap = {
      go: '\u{1F4E6}',
      js: '\u{1F7E8}',
      ts: '\u{1F535}',
      svelte: '\u{1F7E0}',
      json: '\u{1F4CB}',
      md: '\u{1F4DD}',
      py: '\u{1F40D}',
      rs: '\u{2699}\uFE0F',
      html: '\u{1F310}',
      css: '\u{1F3A8}',
      yaml: '\u{2699}\uFE0F',
      yml: '\u{2699}\uFE0F',
      toml: '\u{2699}\uFE0F',
      sh: '\u{1F4DF}',
      sql: '\u{1F5C3}\uFE0F',
      png: '\u{1F5BC}\uFE0F',
      jpg: '\u{1F5BC}\uFE0F',
      jpeg: '\u{1F5BC}\uFE0F',
      gif: '\u{1F5BC}\uFE0F',
      svg: '\u{1F5BC}\uFE0F',
    };
    return iconMap[ext] || '\u{1F4C4}';
  }

  function handleClick(item) {
    if (item.isDir) {
      dispatch('navigate', item);
    } else {
      dispatch('select', item);
    }
  }
</script>

{#if items.length > 0}
  <div class="file-popup">
    {#each items as item, i}
      <div
        class="popup-item"
        class:selected={i === selectedIndex}
        on:mousedown|preventDefault={() => handleClick(item)}
        on:mouseenter={() => selectedIndex = i}
      >
        <span class="popup-icon">{getIcon(item)}</span>
        <span class="popup-name">{item.name}</span>
        <span class="popup-desc">{item.relativePath || ''}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .file-popup {
    position: absolute;
    bottom: 100%;
    left: 0;
    right: 0;
    margin-bottom: 4px;
    background-color: var(--popup-bg);
    border: 1px solid var(--popup-border);
    border-radius: 8px;
    padding: 4px;
    box-shadow: var(--shadow-lg);
    z-index: 100;
    max-height: 280px;
    overflow-y: auto;
    animation: fadeIn 0.15s ease-out;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(4px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .popup-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    border-radius: 6px;
    cursor: pointer;
    transition: background-color 0.1s;
  }

  .popup-item.selected {
    background-color: var(--accent);
  }

  .popup-item:hover {
    background-color: var(--popup-hover);
  }

  .popup-item.selected:hover {
    background-color: var(--accent);
  }

  .popup-icon {
    font-size: 16px;
    flex-shrink: 0;
    width: 24px;
    text-align: center;
  }

  .popup-name {
    color: var(--popup-name);
    font-weight: 600;
    font-size: 13px;
    flex-shrink: 0;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .popup-desc {
    color: var(--popup-desc);
    font-size: 12px;
    margin-left: 4px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .popup-item.selected .popup-name {
    color: var(--text-inverse);
  }

  .popup-item.selected .popup-desc {
    color: rgba(255, 255, 255, 0.8);
  }

  .file-popup::-webkit-scrollbar {
    width: 6px;
  }

  .file-popup::-webkit-scrollbar-track {
    background: transparent;
  }

  .file-popup::-webkit-scrollbar-thumb {
    background: var(--scrollbar-thumb);
    border-radius: 3px;
  }
</style>
