<script>
  import { onMount } from 'svelte';
  import DynamicSplitLayout from './lib/components/DynamicSplitLayout.svelte'
  import { theme } from './lib/stores/theme.js';

  // Apply theme to document element reactively
  $: {
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', $theme);
    }
  }
</script>

<DynamicSplitLayout />

<style>
  /* ===== Light Theme (default) ===== */
  :global([data-theme="light"]) {
    --bg-primary: #f5f5f5;
    --bg-secondary: #ffffff;
    --bg-tertiary: #fafafa;
    --bg-header: #f0f0f0;
    --bg-hover: #e0e0e0;
    --bg-input: #fafafa;
    --bg-panel: #f0f4f8;

    --bg-message-user: #d4e8fc;
    --bg-message-assistant: #e8e8e8;
    --bg-message-system: #e8eaf6;

    --text-primary: #333333;
    --text-secondary: #666666;
    --text-muted: #999999;
    --text-inverse: #ffffff;

    --border-primary: #e0e0e0;
    --border-secondary: #cccccc;

    --accent: #0078d4;
    --accent-hover: #106ebe;
    --accent-bg: rgba(0, 120, 212, 0.08);
    --accent-light: #e3f2fd;

    --error: #e81123;
    --error-bg: #fdecea;
    --error-text: #c62828;
    --success: #2e7d32;

    --scrollbar-track: #fafafa;
    --scrollbar-thumb: #cccccc;
    --scrollbar-thumb-hover: #aaaaaa;

    --shadow-sm: 0 2px 4px rgba(0, 0, 0, 0.05);
    --shadow-md: 0 4px 8px rgba(0, 0, 0, 0.1);
    --shadow-lg: 0 -4px 16px rgba(0, 0, 0, 0.1);

    --code-bg: #f6f8fa;
    --code-border: #e0e0e0;
    --code-text: #1a1a1a;
    --code-block-bg: #f6f8fa;
    --code-btn-bg: #e0e0e0;
    --code-btn-text: #666666;
    --code-btn-border: #cccccc;

    --table-header-bg: #f0f0f0;
    --table-row-even: #fafafa;
    --table-border: #e0e0e0;

    --link-color: #0078d4;
    --blockquote-border: #0078d4;
    --blockquote-text: #666666;
    --hr-color: #e0e0e0;

    --heading-color: #333333;
    --heading-sub-color: #555555;
    --heading-border: #e0e0e0;

    --drag-overlay: rgba(0, 120, 212, 0.3);

    --popup-bg: #ffffff;
    --popup-border: #e0e0e0;
    --popup-hover: #f0f0f0;
    --popup-name: #333333;
    --popup-desc: #999999;

    --file-item-bg: #f5f5f5;
    --placeholder-bg: #f0f0f0;
  }

  /* ===== Dark Theme ===== */
  :global([data-theme="dark"]) {
    --bg-primary: #1e1e1e;
    --bg-secondary: #252526;
    --bg-tertiary: #2d2d2d;
    --bg-header: #333333;
    --bg-hover: #3d3d3d;
    --bg-input: #2d2d2d;
    --bg-panel: #252530;

    --bg-message-user: #1a3a5c;
    --bg-message-assistant: #383838;
    --bg-message-system: #2a2d3a;

    --text-primary: #e0e0e0;
    --text-secondary: #a0a0a0;
    --text-muted: #777777;
    --text-inverse: #ffffff;

    --border-primary: #3d3d3d;
    --border-secondary: #5a5a5a;

    --accent: #0078d4;
    --accent-hover: #1a8ae8;
    --accent-bg: rgba(0, 120, 212, 0.15);
    --accent-light: #264f78;

    --error: #e81123;
    --error-bg: #3d1518;
    --error-text: #ff6b6b;
    --success: #4caf50;

    --scrollbar-track: #1e1e1e;
    --scrollbar-thumb: #4a4a4a;
    --scrollbar-thumb-hover: #5a5a5a;

    --shadow-sm: 0 2px 4px rgba(0, 0, 0, 0.3);
    --shadow-md: 0 4px 8px rgba(0, 0, 0, 0.4);
    --shadow-lg: 0 -4px 16px rgba(0, 0, 0, 0.4);

    --code-bg: #3a3a3a;
    --code-border: #3d3d3d;
    --code-text: #e0e0e0;
    --code-block-bg: #1e1e1e;
    --code-btn-bg: #3a3a3a;
    --code-btn-text: #cccccc;
    --code-btn-border: #5a5a5a;

    --table-header-bg: #2d2d2d;
    --table-row-even: #252525;
    --table-border: #3d3d3d;

    --link-color: #58a6ff;
    --blockquote-border: #58a6ff;
    --blockquote-text: #a0a0a0;
    --hr-color: #3d3d3d;

    --heading-color: #ffffff;
    --heading-sub-color: #cccccc;
    --heading-border: #3d3d3d;

    --drag-overlay: rgba(0, 120, 212, 0.3);

    --popup-bg: #2d2d2d;
    --popup-border: #3d3d3d;
    --popup-hover: #3a3a3a;
    --popup-name: #ffffff;
    --popup-desc: #a0a0a0;

    --file-item-bg: #2d2d2d;
    --placeholder-bg: #3a3a3a;
  }

  :global(body) {
    margin: 0;
    padding: 0;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Roboto', 'Oxygen',
      'Ubuntu', 'Cantarell', 'Fira Sans', 'Droid Sans', 'Helvetica Neue',
      sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    overflow: hidden;
    background-color: var(--bg-primary);
    color: var(--text-primary);
  }

  :global(*) {
    box-sizing: border-box;
  }

  /* Global scrollbar styling */
  :global(::-webkit-scrollbar) {
    width: 8px;
    height: 8px;
  }

  :global(::-webkit-scrollbar-track) {
    background: var(--scrollbar-track);
  }

  :global(::-webkit-scrollbar-thumb) {
    background: var(--scrollbar-thumb);
    border-radius: 4px;
  }

  :global(::-webkit-scrollbar-thumb:hover) {
    background: var(--scrollbar-thumb-hover);
  }

  /* Selection color */
  :global(::selection) {
    background-color: var(--accent);
    color: #ffffff;
  }

  :global(::-moz-selection) {
    background-color: var(--accent);
    color: #ffffff;
  }
</style>
