<script>
  import { onMount } from 'svelte';
  import { marked } from 'marked';
  import hljs from 'highlight.js';
  import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime.js';

  export let content = '';

  let renderedHTML = '';

  // Custom renderer for diff blocks and GitHub-style alerts
  const renderer = new marked.Renderer();

  // Override blockquote to support GitHub alerts: > [!NOTE], > [!WARNING], etc.
  const defaultBlockquote = renderer.blockquote.bind(renderer);
  renderer.blockquote = function(body) {
    // body can be an object with .text or a string
    const text = typeof body === 'object' ? (body.text || '') : String(body);
    const alertMatch = text.match(/^\s*<p>\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*<br\s*\/?>\s*/i)
      || text.match(/^\s*<p>\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n/i);
    if (alertMatch) {
      const type = alertMatch[1].toLowerCase();
      const icons = { note: 'ℹ️', tip: '💡', important: '❗', warning: '⚠️', caution: '🔴' };
      const labels = { note: 'Note', tip: 'Tip', important: 'Important', warning: 'Warning', caution: 'Caution' };
      const cleanBody = text.replace(alertMatch[0], '<p>');
      return `<div class="md-alert md-alert-${type}"><div class="md-alert-title">${icons[type] || ''} ${labels[type]}</div>${cleanBody}</div>`;
    }
    return `<blockquote>${text}</blockquote>`;
  };

  // Configure marked
  marked.setOptions({
    highlight: function(code, lang) {
      if (lang && hljs.getLanguage(lang)) {
        try {
          return hljs.highlight(code, { language: lang }).value;
        } catch (err) {
          console.error('Highlight error:', err);
        }
      }
      return hljs.highlightAuto(code).value;
    },
    renderer,
    breaks: true,
    gfm: true,
  });

  $: {
    try {
      renderedHTML = marked.parse(content || '');
    } catch (err) {
      console.error('Markdown parsing error:', err);
      renderedHTML = content;
    }
  }

  let containerEl;

  function interceptLinks() {
    if (!containerEl) return;
    containerEl.querySelectorAll('a[href]').forEach((a) => {
      if (a.dataset.intercepted) return;
      a.dataset.intercepted = 'true';
      a.addEventListener('click', (e) => {
        e.preventDefault();
        const href = a.getAttribute('href');
        if (href && (href.startsWith('http://') || href.startsWith('https://'))) {
          BrowserOpenURL(href);
        }
      });
    });
  }

  function addCopyButtons() {
    // Add copy buttons to code blocks
    document.querySelectorAll('.markdown-content pre').forEach((pre) => {
      // Skip if already has copy button
      if (pre.querySelector('.copy-btn')) return;

      const button = document.createElement('button');
      button.className = 'copy-btn';
      button.textContent = '복사';
      button.onclick = () => {
        const code = pre.querySelector('code');
        if (code) {
          navigator.clipboard.writeText(code.textContent).then(() => {
            button.textContent = '✓ 복사됨';
            setTimeout(() => {
              button.textContent = '복사';
            }, 2000);
          });
        }
      };
      pre.appendChild(button);
    });
  }

  onMount(() => {
    // Apply syntax highlighting to any existing code blocks
    document.querySelectorAll('pre code').forEach((block) => {
      hljs.highlightElement(block);
    });
    addCopyButtons();
    interceptLinks();
  });

  $: if (renderedHTML) {
    // Re-apply highlighting, copy buttons, and link interception when content changes
    setTimeout(() => {
      document.querySelectorAll('.markdown-content pre code').forEach((block) => {
        hljs.highlightElement(block);
      });
      addCopyButtons();
      interceptLinks();
    }, 0);
  }
</script>

<div class="markdown-content" bind:this={containerEl}>
  {@html renderedHTML}
</div>

<style>
  .markdown-content {
    color: var(--text-primary);
    line-height: 1.5;
    text-align: left;
  }

  /* Headings */
  .markdown-content :global(h1) {
    font-size: 1.6em;
    font-weight: bold;
    margin: 0.8em 0 0.4em 0;
    color: var(--heading-color);
    border-bottom: 2px solid var(--heading-border);
    padding-bottom: 0.3em;
  }

  .markdown-content :global(h2) {
    font-size: 1.4em;
    font-weight: bold;
    margin: 0.6em 0 0.3em 0;
    color: var(--heading-color);
    border-bottom: 1px solid var(--heading-border);
    padding-bottom: 0.2em;
  }

  .markdown-content :global(h3) {
    font-size: 1.2em;
    font-weight: bold;
    margin: 0.5em 0 0.2em 0;
    color: var(--heading-sub-color);
  }

  .markdown-content :global(h4),
  .markdown-content :global(h5),
  .markdown-content :global(h6) {
    font-size: 1.1em;
    font-weight: bold;
    margin: 0.4em 0 0.2em 0;
    color: var(--heading-sub-color);
  }

  /* Paragraphs */
  .markdown-content :global(p) {
    margin: 0.3em 0;
  }

  /* Lists */
  .markdown-content :global(ul),
  .markdown-content :global(ol) {
    margin: 0.3em 0;
    padding-left: 1.5em;
  }

  .markdown-content :global(li) {
    margin: 0.1em 0;
  }

  /* Links */
  .markdown-content :global(a) {
    color: var(--link-color);
    text-decoration: none;
  }

  .markdown-content :global(a:hover) {
    text-decoration: underline;
  }

  /* Inline code */
  .markdown-content :global(code:not(pre code)) {
    background-color: var(--code-bg);
    color: var(--code-text);
    padding: 0.2em 0.4em;
    border-radius: 3px;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
  }

  /* Code blocks */
  .markdown-content :global(pre) {
    background-color: var(--code-block-bg);
    border: 1px solid var(--code-border);
    border-radius: 6px;
    padding: 0.8em;
    overflow-x: auto;
    margin: 0.6em 0;
    position: relative;
  }

  .markdown-content :global(pre .copy-btn) {
    position: absolute;
    top: 0.5em;
    right: 0.5em;
    padding: 0.3em 0.6em;
    background-color: var(--code-btn-bg);
    color: var(--code-btn-text);
    border: 1px solid var(--code-btn-border);
    border-radius: 4px;
    cursor: pointer;
    font-size: 0.8em;
    transition: all 0.2s;
  }

  .markdown-content :global(pre .copy-btn:hover) {
    background-color: var(--bg-hover);
  }

  .markdown-content :global(pre code) {
    background: none;
    color: inherit;
    padding: 0;
    border-radius: 0;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
    line-height: 1.5;
  }

  /* Blockquotes */
  .markdown-content :global(blockquote) {
    border-left: 4px solid var(--blockquote-border);
    padding-left: 1em;
    margin: 0.6em 0;
    color: var(--blockquote-text);
    font-style: italic;
  }

  /* Tables */
  .markdown-content :global(table) {
    border-collapse: collapse;
    width: 100%;
    margin: 0.6em 0;
    display: block;
    overflow-x: auto;
  }

  .markdown-content :global(th),
  .markdown-content :global(td) {
    border: 1px solid var(--table-border);
    padding: 0.6em 1em;
    text-align: left;
  }

  .markdown-content :global(th) {
    background-color: var(--table-header-bg);
    font-weight: bold;
  }

  .markdown-content :global(tr:nth-child(even)) {
    background-color: var(--table-row-even);
  }

  /* Horizontal rule */
  .markdown-content :global(hr) {
    border: none;
    border-top: 1px solid var(--hr-color);
    margin: 0.8em 0;
  }

  /* Images */
  .markdown-content :global(img) {
    max-width: 100%;
    height: auto;
    border-radius: 6px;
    margin: 0.4em 0;
  }

  /* Task lists */
  .markdown-content :global(input[type="checkbox"]) {
    margin-right: 0.5em;
  }

  /* Diff syntax highlighting */
  .markdown-content :global(pre code.language-diff .hljs-addition),
  .markdown-content :global(pre code.language-diff .addition) {
    color: #22c55e;
    background-color: rgba(34, 197, 94, 0.1);
    display: inline-block;
    width: 100%;
  }

  .markdown-content :global(pre code.language-diff .hljs-deletion),
  .markdown-content :global(pre code.language-diff .deletion) {
    color: #ef4444;
    background-color: rgba(239, 68, 68, 0.1);
    display: inline-block;
    width: 100%;
  }

  /* Fallback: line-level diff coloring for non-hljs rendered diffs */
  .markdown-content :global(pre code.language-diff) {
    white-space: pre;
  }

  /* GitHub-style alert boxes */
  .markdown-content :global(.md-alert) {
    padding: 10px 14px;
    margin: 0.6em 0;
    border-radius: 6px;
    border-left: 4px solid;
    font-size: 0.95em;
  }

  .markdown-content :global(.md-alert-title) {
    font-weight: 600;
    margin-bottom: 4px;
    font-size: 0.9em;
  }

  .markdown-content :global(.md-alert p) {
    margin: 0.2em 0;
  }

  .markdown-content :global(.md-alert-note) {
    border-color: #3b82f6;
    background-color: rgba(59, 130, 246, 0.08);
  }
  .markdown-content :global(.md-alert-note .md-alert-title) {
    color: #3b82f6;
  }

  .markdown-content :global(.md-alert-tip) {
    border-color: #22c55e;
    background-color: rgba(34, 197, 94, 0.08);
  }
  .markdown-content :global(.md-alert-tip .md-alert-title) {
    color: #22c55e;
  }

  .markdown-content :global(.md-alert-important) {
    border-color: #a855f7;
    background-color: rgba(168, 85, 247, 0.08);
  }
  .markdown-content :global(.md-alert-important .md-alert-title) {
    color: #a855f7;
  }

  .markdown-content :global(.md-alert-warning) {
    border-color: #f59e0b;
    background-color: rgba(245, 158, 11, 0.08);
  }
  .markdown-content :global(.md-alert-warning .md-alert-title) {
    color: #f59e0b;
  }

  .markdown-content :global(.md-alert-caution) {
    border-color: #ef4444;
    background-color: rgba(239, 68, 68, 0.08);
  }
  .markdown-content :global(.md-alert-caution .md-alert-title) {
    color: #ef4444;
  }

</style>
