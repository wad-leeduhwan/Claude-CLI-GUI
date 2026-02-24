<script>
  import { createEventDispatcher } from 'svelte';

  export let commands = [];
  export let selectedIndex = 0;

  const dispatch = createEventDispatcher();

  function handleSelect(command) {
    dispatch('select', command);
  }
</script>

{#if commands.length > 0}
  <div class="slash-popup">
    {#each commands as cmd, i}
      <div
        class="popup-item"
        class:selected={i === selectedIndex}
        on:mousedown|preventDefault={() => handleSelect(cmd)}
        on:mouseenter={() => selectedIndex = i}
      >
        <span class="popup-icon">{cmd.icon}</span>
        <span class="popup-name">/{cmd.name}</span>
        <span class="popup-desc">{cmd.description}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .slash-popup {
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
    max-height: 240px;
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
  }

  .popup-desc {
    color: var(--popup-desc);
    font-size: 12px;
    margin-left: 4px;
  }

  .popup-item.selected .popup-name {
    color: var(--text-inverse);
  }

  .popup-item.selected .popup-desc {
    color: rgba(255, 255, 255, 0.8);
  }

  .slash-popup::-webkit-scrollbar {
    width: 6px;
  }

  .slash-popup::-webkit-scrollbar-track {
    background: transparent;
  }

  .slash-popup::-webkit-scrollbar-thumb {
    background: var(--scrollbar-thumb);
    border-radius: 3px;
  }
</style>
