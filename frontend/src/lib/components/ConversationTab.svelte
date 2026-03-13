<script>
  import { createEventDispatcher, onDestroy, onMount, afterUpdate, tick } from 'svelte';
  import { SendMessage, SendAdminMessage, SendTeamsMessage, CancelMessage, CancelOrchestrationJob, RemoveLastUserMessage, TruncateMessages, ToggleTabAdminMode, ToggleTabPlanMode, ToggleTabTeamsMode, SelectFiles, GetFileInfo, GetCurrentModel, SetModel, GetAvailableModels, SaveDroppedImage, SaveDroppedFile, ReadFileSnippet, CompletePath, SearchFiles, OpenExpandedView, IsGitRepo, AnswerQuestion, RequestCodeReview, GetPlanContent } from '../../../wailsjs/go/main/App';
  import MarkdownViewer from './MarkdownViewer.svelte';
  import SlashCommandPopup from './SlashCommandPopup.svelte';
  import FilePathPopup from './FilePathPopup.svelte';
  import { streamingStore } from '../stores/streaming.js';
  import { orchestratorStore } from '../stores/orchestrator.js';
  import { modelStore } from '../stores/model.js';
  import { findCommand, matchCommands, getAllCommands } from '../commands/registry.js';
  import { focusedTabId } from '../stores/focusedTab.js';
  import '../../assets/hljs-github-dark.css';

  export let tabId;
  export let tabName;
  export let messages = [];
  export let adminMode = false;
  export let teamsMode = false;
  export let planMode = false;
  export let workDir = '';

  let inputMessage = '';
  let inputHistory = [];      // 보낸 메시지 히스토리 배열
  let historyIndex = -1;      // 현재 히스토리 위치 (-1 = 새 입력 모드)
  let historySavedInput = '';  // 히스토리 탐색 시작 전 입력값 임시 저장
  let sending = false;
  let messagesArea;
  let attachedFiles = []; // Array of {path: string, name: string, preview: string}
  let dragOver = false;
  let errorMessage = '';
  let displayMessages = [];


  // Search state
  let searchOpen = false;
  let searchQuery = '';
  let searchMatches = [];
  let searchCurrentIndex = -1;
  let searchInputEl;

  // Image preview cache: path -> data URL
  let imagePreviewCache = {};

  // Component lifecycle tracking
  let componentActive = true;   // tracks whether component is destroyed
  let restoredStreaming = false; // distinguishes store-restored streaming from fresh send

  // Typing effect state
  let typingContent = '';
  let typingTimer = null;
  let fullContent = '';

  // Streaming content overflow detection
  let streamingWrapperEl = null;
  let streamingBodyEl = null;
  let streamingOverflowing = false;

  afterUpdate(() => {
    if (streamingWrapperEl && streamingBodyEl) {
      streamingOverflowing = streamingBodyEl.scrollHeight > streamingWrapperEl.clientHeight;
      if (streamingOverflowing) {
        streamingWrapperEl.scrollTop = streamingWrapperEl.scrollHeight;
      }
    }
  });

  // Tool activity history toggle
  let expandedProcessIndex = null;
  function toggleProcess(index) {
    expandedProcessIndex = expandedProcessIndex === index ? null : index;
  }

  // Slash command popup state
  let showPopup = false;
  let popupCommands = [];
  let popupSelectedIndex = 0;

  // @ file path popup state
  let showFilePopup = false;
  let filePopupItems = [];
  let filePopupSelectedIndex = 0;
  let atTokenStart = -1;
  let textareaEl;

  // CLI panel state (interactive area between messages and input)
  let cliPanel = null; // null | { type: 'model-select', models, currentModel } | { type: 'result', content }
  let cliPanelSelectedIndex = 0;
  let cliPanelTimer = null;
  let choiceDismissed = false; // user explicitly closed the choice panel
  let cliPanelEl; // focusable cli-panel element reference

  // Image lightbox state
  let lightboxSrc = null; // data URL of image to show full-size

  // Streaming elapsed time
  let streamingStartTime = null;
  let streamingElapsed = 0;
  let streamingElapsedTimer = null;

  // Track whether a response was received during the current plan mode session
  let planResponseReceived = false;

  // Git repo detection for commit button
  let isGitRepo = false;
  $: if (workDir) {
    IsGitRepo(workDir).then(v => { isGitRepo = v; }).catch(() => { isGitRepo = false; });
  }

  // Orchestrator state for admin mode
  $: orchestratorState = $orchestratorStore[tabId] || null;

  // Auto-scroll: only scroll on explicit triggers, never fight user scroll
  let autoScrollEnabled = true;
  let programmaticScroll = false; // guard to ignore scroll events from our own scrollToBottom()

  function scrollToBottom() {
    if (messagesArea && autoScrollEnabled) {
      programmaticScroll = true;
      messagesArea.scrollTop = messagesArea.scrollHeight;
      // Reset flag after browser processes the scroll event
      requestAnimationFrame(() => { programmaticScroll = false; });
    }
  }

  function handleMessagesScroll() {
    if (!messagesArea || programmaticScroll) return;
    const threshold = 150;
    const nearBottom = messagesArea.scrollHeight - messagesArea.scrollTop - messagesArea.clientHeight < threshold;
    // Only disable auto-scroll on user-initiated scroll away from bottom during streaming
    if (sending && !nearBottom) {
      autoScrollEnabled = false;
    }
  }

  // Placeholder cycling state
  const placeholders = [
    "메시지를 입력하세요... (Cmd/Ctrl + Enter로 전송)",
    "/help — 사용 가능한 명령어 보기",
    "/usage — 토큰 사용량 확인",
  ];
  let placeholderIndex = 0;
  let currentPlaceholder = placeholders[0];
  let placeholderTimer = null;

  const dispatch = createEventDispatcher();
  const isMac = navigator.platform.toUpperCase().includes('MAC');

  onMount(() => {
    // Set this tab as focused if no tab is focused yet
    if (!$focusedTabId) {
      focusedTabId.set(tabId);
    }

    placeholderTimer = setInterval(() => {
      placeholderIndex = (placeholderIndex + 1) % placeholders.length;
      currentPlaceholder = placeholders[placeholderIndex];
    }, 5000);

    // Restore streaming state if this tab is actively streaming
    const storeState = $streamingStore[tabId];
    if (storeState?.isStreaming) {
      sending = true;
      restoredStreaming = true;
      startStreamingTimer();
      autoScrollEnabled = true;
      // reactive statement ($: streamingContent) will automatically pick up store content and start typing effect
    }
  });

  // Get streaming state directly from global store
  $: streamingContent = $streamingStore[tabId]?.content || '';
  $: toolActivity = $streamingStore[tabId]?.toolActivity || null;
  $: tokenUsage = $streamingStore[tabId]?.tokenUsage || null;
  $: console.log(`[ConversationTab ${tabId}] Store updated - content length: ${streamingContent.length}`);

  // When streaming content changes, start typing effect
  $: if (streamingContent && streamingContent !== fullContent) {
    startTypingEffect(streamingContent);
  }

  // Reactive plan-ready detection
  $: {
    const lastMsg = displayMessages.length > 0
      ? displayMessages[displayMessages.length - 1] : null;
    const lastContent = lastMsg?.role === 'assistant' ? (lastMsg.content || '') : '';
    const isAsking = lastContent && isAssistantAsking(lastContent);
    const planReady = planMode && !sending && planResponseReceived && lastMsg && lastMsg.role === 'assistant' && !isAsking;

    if (planReady && (!cliPanel || cliPanel.type === 'plan-ready')) {
      cliPanel = { type: 'plan-ready' };
    } else if (!planReady && cliPanel && cliPanel.type === 'plan-ready') {
      cliPanel = null;
    }
  }

  // Reactive AskUserQuestion detection from streaming store
  $: {
    const pq = $streamingStore[tabId]?.pendingQuestion;
    if (pq && (!cliPanel || cliPanel.type !== 'ask-user-question' || cliPanel.toolUseId !== pq.toolUseId)) {
      cliPanel = {
        type: 'ask-user-question',
        questions: pq.questions,
        toolUseId: pq.toolUseId,
        currentStep: 0,
        answers: {},
        selectedIndex: 0,
        reviewing: false,
        customText: '',
        showCustomInput: false
      };
    }
  }

  // Clear UI state when workDir changes (e.g., user switches project directory)
  let lastWorkDir = workDir;
  $: if (workDir !== lastWorkDir) {
    console.log(`[ConversationTab ${tabId}] WorkDir changed: ${lastWorkDir} → ${workDir}`);
    lastWorkDir = workDir;
    displayMessages = [];
    lastMessagesLength = 0;
    sending = false;
    stopTypingEffect();
    stopStreamingTimer();
    streamingStore.clear(tabId);
  }

  // Force reactivity when messages prop changes
  // Only update when messages array actually changes (not on every render)
  let lastMessagesLength = 0;
  $: if (messages.length !== lastMessagesLength) {
    console.log(`[ConversationTab ${tabId}] Messages updated:`, messages.length);
    const prevLength = lastMessagesLength;
    lastMessagesLength = messages.length;
    displayMessages = [...messages];
    // Scroll after message update (only if user hasn't scrolled up)
    setTimeout(() => scrollToBottom(), 0);

    // When a restored streaming finishes and the assistant response arrives, clean up
    if (restoredStreaming && messages.length > prevLength && prevLength > 0) {
      const lastMsg = messages[messages.length - 1];
      if (lastMsg && lastMsg.role === 'assistant') {
        restoredStreaming = false;
        sending = false;
        stopStreamingTimer();
        stopTypingEffect();
        streamingStore.clear(tabId);
      }
    }
  }


  function startTypingEffect(fullText) {
    // Stop previous typing if any
    if (typingTimer) {
      clearInterval(typingTimer);
    }

    fullContent = fullText;

    // If we already have typed content, continue from where we left off
    // instead of resetting (new chunks are full replacements, not deltas)
    let currentIndex = typingContent.length;

    // If new text is shorter or completely different, reset
    if (!fullText.startsWith(typingContent)) {
      typingContent = '';
      currentIndex = 0;
    }

    const charsPerTick = 5; // Type 5 characters at a time for faster catch-up
    const tickInterval = 20; // 20ms between ticks = ~250 chars/second

    typingTimer = setInterval(() => {
      if (currentIndex >= fullText.length) {
        clearInterval(typingTimer);
        typingTimer = null;
        typingContent = fullText; // Ensure we show the complete text
        return;
      }

      // Add next few characters
      const endIndex = Math.min(currentIndex + charsPerTick, fullText.length);
      typingContent = fullText.substring(0, endIndex);
      currentIndex = endIndex;
      scrollToBottom();
    }, tickInterval);
  }

  function stopTypingEffect() {
    if (typingTimer) {
      clearInterval(typingTimer);
      typingTimer = null;
    }
    typingContent = '';
    fullContent = '';
  }

  function startStreamingTimer() {
    streamingStartTime = Date.now();
    streamingElapsed = 0;
    streamingElapsedTimer = setInterval(() => {
      streamingElapsed = Math.floor((Date.now() - streamingStartTime) / 1000);
    }, 1000);
  }

  function stopStreamingTimer() {
    if (streamingElapsedTimer) {
      clearInterval(streamingElapsedTimer);
      streamingElapsedTimer = null;
    }
    streamingStartTime = null;
  }

  function formatElapsed(sec) {
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    return m > 0 ? `${m}:${s.toString().padStart(2, '0')}` : `${s}s`;
  }

  function getToolIcon(name) {
    return { Read:'📖', Write:'📁', Edit:'✏️', Bash:'💻', Glob:'🔍', Grep:'🔎',
             WebSearch:'🌐', WebFetch:'🌐', Task:'📋' }[name] || '⚙️';
  }

  function getToolLabel(name) {
    return { Read:'Reading', Write:'Writing', Edit:'Editing', Bash:'Running',
             Glob:'Searching', Grep:'Searching', WebSearch:'Searching',
             WebFetch:'Fetching', Task:'Task' }[name] || name;
  }

  function formatTokens(n) {
    if (!n || n <= 0) return '0';
    if (n >= 1000000) {
      const v = n / 1000000;
      return (v >= 10 ? Math.round(v) : v.toFixed(1)) + 'm';
    }
    if (n >= 1000) {
      const v = n / 1000;
      return (v >= 10 ? Math.round(v) : v.toFixed(1)) + 'k';
    }
    return n.toString();
  }

  function formatDuration(ms) {
    if (!ms || ms <= 0) return '';
    const sec = Math.floor(ms / 1000);
    const m = Math.floor(sec / 60);
    const s = sec % 60;
    if (m > 0) return `${m}분 ${s}초`;
    return `${s}초`;
  }

  let copiedIndex = null;
  let copiedTimer = null;

  // Quick reply choices parsed from last assistant message
  let quickReplies = { question: '', choices: [] };

  function parseQuickReplies(msgs) {
    if (!msgs || msgs.length === 0 || sending) return { question: '', choices: [] };
    // Find the last assistant message
    for (let i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].role === 'assistant') {
        return extractChoices(msgs[i].content);
      }
    }
    return { question: '', choices: [] };
  }

  function extractChoices(text) {
    if (!text) return { question: '', choices: [] };
    const lines = text.split('\n');
    const choices = [];
    let choiceStartIndex = -1;

    // Pass 1: Scan from end for numbered options (highest priority)
    let foundEnd = false;
    for (let i = lines.length - 1; i >= 0; i--) {
      const line = lines[i].trim();
      if (!line) {
        if (foundEnd) continue;
        continue;
      }
      const numMatch = line.match(/^(?:[-*]\s*)?(\d+)[.):\]]\s*(.+)$/);
      if (numMatch) {
        choices.unshift({ num: numMatch[1], text: numMatch[2].trim() });
        choiceStartIndex = i;
        foundEnd = true;
      } else {
        if (foundEnd) break;
        if (lines.length - i > 20) break;
      }
    }

    // Pass 2: If no numbered choices, try bullet patterns as fallback
    if (choices.length < 2) {
      choices.length = 0;
      choiceStartIndex = -1;
      foundEnd = false;
      for (let i = lines.length - 1; i >= 0; i--) {
        const line = lines[i].trim();
        if (!line) {
          if (foundEnd) continue;
          continue;
        }
        const numMatch = line.match(/^(?:[-*]\s*)?(\d+)[.):\]]\s*(.+)$/);
        if (numMatch) {
          choices.unshift({ num: numMatch[1], text: numMatch[2].trim() });
          choiceStartIndex = i;
          foundEnd = true;
        } else {
          const bulletMatch = line.match(/^[-*]\s+(.+)$/);
          if (bulletMatch) {
            choices.unshift({ num: null, text: bulletMatch[1].trim() });
            choiceStartIndex = i;
            foundEnd = true;
          } else {
            break; // 불릿은 반드시 메시지 끝에 위치해야 함
          }
        }
      }
      // Auto-assign numbers to bullet-only choices
      if (choices.some(c => c.num === null)) {
        choices.forEach((c, i) => { if (c.num === null) c.num = String(i + 1); });
      }
    }

    // Need 2+ choices
    if (choices.length < 2) return { question: '', choices: [] };

    // Look for a question line above the choices
    // Search upwards from choiceStartIndex for a line ending with ? or Korean question markers
    let question = '';
    for (let i = choiceStartIndex - 1; i >= 0 && i >= choiceStartIndex - 5; i--) {
      const line = lines[i].trim();
      if (!line) continue;
      // Strip markdown inline formatting for pattern matching
      const stripped = line.replace(/[*_`~]+/g, '').trim();
      // Check if line looks like a question (ends with ?, 까요?, 세요?, 나요?, etc.)
      if (/[?？]$/.test(stripped) || /(?:까요|세요|나요|할까|는지|을까|ㄹ까)\s*[.?]?\s*$/.test(stripped)) {
        question = stripped;
        break;
      }
      // Also accept lines that look like a prompt (e.g., "선택해주세요", "골라주세요")
      if (/(?:선택|골라|알려|결정|지정)/.test(stripped) && stripped.length < 100) {
        question = stripped;
        break;
      }
    }

    // If no question found, these are likely explanatory steps, not choices
    if (!question) return { question: '', choices: [] };

    return { question, choices };
  }

  function isAssistantAsking(text) {
    if (!text) return false;
    const lines = text.trim().split('\n');
    // Check if message ends with numbered choices (2+ consecutive)
    let choiceCount = 0;
    for (let i = lines.length - 1; i >= 0; i--) {
      const line = lines[i].trim();
      if (!line) continue;
      if (/^(?:[-*]\s*)?(\d+)[.):\]]\s*.+$/.test(line)) {
        choiceCount++;
      } else {
        break;
      }
    }
    if (choiceCount >= 2) return true;

    // Check if ends with question pattern
    const lastNonEmpty = [...lines].reverse().find(l => l.trim());
    if (!lastNonEmpty) return false;
    const trimmed = lastNonEmpty.trim();
    if (/[?？]\s*$/.test(trimmed)) return true;
    if (/(?:까요|세요|나요|할까|는지|을까|ㄹ까|주세요)\s*[.?]?\s*$/.test(trimmed)) return true;
    return false;
  }

  // Reactively parse quick replies from displayed messages and show as CLI panel
  $: {
    const parsed = !sending ? parseQuickReplies(displayMessages) : { question: '', choices: [] };
    quickReplies = parsed;
    if (parsed.choices.length > 0 && !choiceDismissed) {
      if (!cliPanel || cliPanel.type === 'plan-ready' || (cliPanel.type === 'choice' && cliPanel.question !== parsed.question)) {
        cliPanel = { type: 'choice', question: parsed.question, choices: parsed.choices, showCustomInput: false, customText: '' };
        cliPanelSelectedIndex = 0;
      }
    } else if (parsed.choices.length === 0) {
      if (cliPanel?.type === 'choice') cliPanel = null;
      choiceDismissed = false; // reset when choices disappear (new messages arrive)
    }
  }

  // Auto-focus cliPanel when navigable choices appear
  $: if (cliPanel && (cliPanel.type === 'choice' || cliPanel.type === 'model-select' || cliPanel.type === 'ask-user-question')) {
    tick().then(() => cliPanelEl?.focus());
  }

  // Re-run search when messages change
  $: if (searchOpen && searchQuery && displayMessages) {
    setTimeout(() => performSearch(), 50);
  }

  // Input area resize state
  let inputAreaHeight = 180; // default px
  let inputResizing = false;
  let inputResizeStartY = 0;
  let inputResizeStartHeight = 0;
  let conversationTabEl;

  function startInputResize(event) {
    event.preventDefault();
    inputResizing = true;
    inputResizeStartY = event.clientY;
    inputResizeStartHeight = inputAreaHeight;
    document.body.classList.add('input-resizing');
  }

  function handleInputResizeMove(event) {
    if (!inputResizing) return;
    const delta = inputResizeStartY - event.clientY;
    const containerHeight = conversationTabEl ? conversationTabEl.offsetHeight : 600;
    const minHeight = 100;
    const maxHeight = containerHeight * 0.7;
    inputAreaHeight = Math.max(minHeight, Math.min(maxHeight, inputResizeStartHeight + delta));
  }

  function stopInputResize() {
    if (!inputResizing) return;
    inputResizing = false;
    document.body.classList.remove('input-resizing');
  }

  function copyMessageContent(content, index) {
    navigator.clipboard.writeText(content).then(() => {
      copiedIndex = index;
      if (copiedTimer) clearTimeout(copiedTimer);
      copiedTimer = setTimeout(() => { copiedIndex = null; }, 1500);
    });
  }

  function openExpandedView(content) {
    OpenExpandedView(content).catch(err => {
      console.error('Failed to open expanded view:', err);
    });
  }

  async function handleSlashCommand(trimmed) {
    const parts = trimmed.substring(1).split(/\s+/);
    const cmdName = parts[0];
    const args = parts.slice(1).join(' ');
    const command = findCommand(cmdName);

    if (!command) return false;

    inputMessage = '';
    showPopup = false;

    // /model without args → interactive selector
    if (cmdName === 'model' && (!args || !args.trim())) {
      try {
        const current = await GetCurrentModel();
        const available = await GetAvailableModels();
        openModelSelector(available, current);
      } catch (error) {
        showCliResult(`**오류:** ${error}`);
      }
      return true;
    }

    // All other commands (including /model with args)
    try {
      const result = await command.handler(tabId, args);

      // Refresh model store if /model command was used with args
      if (cmdName === 'model' && args && args.trim()) {
        modelStore.refresh();
      }

      if (result.type === 'plan-execute') {
        if (planMode) {
          await executePlan();
        } else {
          showCliResult('Plan 모드가 활성화되어 있지 않습니다.');
        }
      } else if (result.type === 'action' && result.content === 'clear') {
        displayMessages = [...displayMessages, { role: 'divider' }];
        lastMessagesLength = displayMessages.length;
      } else if (result.type === 'attachment') {
        await addFile(result.data.path);
        if (result.data.lineRange) {
          const last = attachedFiles.length - 1;
          attachedFiles[last].lineRange = result.data.lineRange;
          attachedFiles = attachedFiles;
        }
      } else if (result.type === 'plan-view') {
        cliPanel = { type: 'plan-viewer', content: result.content };
      } else if (result.type === 'message') {
        showCliResult(result.content);
      }
    } catch (error) {
      showCliResult(`**오류:** ${error}`);
    }

    return true;
  }

  // --- CLI panel helpers ---

  function openModelSelector(models, currentModel) {
    closeCliPanel();
    cliPanel = { type: 'model-select', models, currentModel };
    cliPanelSelectedIndex = Math.max(0, models.indexOf(currentModel));
  }

  function showCliResult(content) {
    closeCliPanel();
    cliPanel = { type: 'result', content };
    autoCloseCliPanel(4000);
  }

  function closeCliPanel() {
    if (cliPanel?.type === 'choice') choiceDismissed = true;
    if (cliPanel?.type === 'ask-user-question') {
      // Clear pendingQuestion from streaming store to prevent reactive re-trigger
      streamingStore.clear(tabId);
    }
    cliPanel = null;
    cliPanelSelectedIndex = 0;
    if (cliPanelTimer) {
      clearTimeout(cliPanelTimer);
      cliPanelTimer = null;
    }
    tick().then(() => textareaEl?.focus());
  }

  function autoCloseCliPanel(ms) {
    if (cliPanelTimer) clearTimeout(cliPanelTimer);
    cliPanelTimer = setTimeout(closeCliPanel, ms);
  }

  async function selectModel() {
    if (!cliPanel || cliPanel.type !== 'model-select') return;
    const selected = cliPanel.models[cliPanelSelectedIndex];
    try {
      await SetModel(selected);
      modelStore.set(selected);
      cliPanel = { type: 'result', content: `모델 변경 완료: **${selected}**` };
      autoCloseCliPanel(2000);
    } catch (error) {
      cliPanel = { type: 'result', content: `**오류:** ${error}` };
      autoCloseCliPanel(3000);
    }
  }

  function selectChoice() {
    if (!cliPanel || cliPanel.type !== 'choice') return;
    // "Other" option selected
    if (cliPanelSelectedIndex === cliPanel.choices.length) {
      cliPanel.showCustomInput = true;
      cliPanel.customText = '';
      cliPanel = cliPanel;
      tick().then(() => {
        const el = document.querySelector('.cli-custom-input');
        if (el) el.focus();
      });
      return;
    }
    const choice = cliPanel.choices[cliPanelSelectedIndex];
    if (!choice) return;
    cliPanel = null;
    quickReplies = { question: '', choices: [] };
    inputMessage = choice.num + ') ' + choice.text;
    tick().then(() => {
      textareaEl?.focus();
      handleSend();
    });
  }

  function submitChoiceCustom() {
    if (!cliPanel || cliPanel.type !== 'choice') return;
    const text = (cliPanel.customText || '').trim();
    if (!text) return;
    cliPanel = null;
    quickReplies = { question: '', choices: [] };
    inputMessage = text;
    tick().then(() => {
      textareaEl?.focus();
      handleSend();
    });
  }

  // --- AskUserQuestion handlers ---

  function pickAskOption(idx) {
    if (!cliPanel || cliPanel.type !== 'ask-user-question') return;
    const q = cliPanel.questions[cliPanel.currentStep];
    cliPanel.answers[q.question] = q.options[idx].label;
    // Move to next question or enter review mode
    if (cliPanel.currentStep < cliPanel.questions.length - 1) {
      cliPanel.currentStep++;
      cliPanel.selectedIndex = 0;
    } else {
      cliPanel.reviewing = true;
    }
    cliPanel = cliPanel; // trigger reactivity
  }

  function pickAskCustom() {
    if (!cliPanel || cliPanel.type !== 'ask-user-question') return;
    cliPanel.showCustomInput = true;
    cliPanel.customText = '';
    cliPanel = cliPanel; // trigger reactivity
    tick().then(() => {
      const el = document.querySelector('.ask-custom-input');
      if (el) el.focus();
    });
  }

  function submitCustomAnswer() {
    if (!cliPanel || cliPanel.type !== 'ask-user-question') return;
    const text = (cliPanel.customText || '').trim();
    if (!text) return;
    const q = cliPanel.questions[cliPanel.currentStep];
    cliPanel.answers[q.question] = text;
    cliPanel.showCustomInput = false;
    cliPanel.customText = '';
    // Move to next question or enter review mode
    if (cliPanel.currentStep < cliPanel.questions.length - 1) {
      cliPanel.currentStep++;
      cliPanel.selectedIndex = 0;
    } else {
      cliPanel.reviewing = true;
    }
    cliPanel = cliPanel;
  }

  function allAskQuestionsAnswered() {
    if (!cliPanel || cliPanel.type !== 'ask-user-question') return false;
    return cliPanel.questions.every(q => cliPanel.answers[q.question]);
  }

  async function submitAskAnswers() {
    if (!allAskQuestionsAnswered()) return;
    const answers = { ...cliPanel.answers };
    cliPanel = null;
    streamingStore.clear(tabId);
    sending = true;
    autoScrollEnabled = true;
    startStreamingTimer();
    try {
      await AnswerQuestion(tabId, answers);
    } catch (e) {
      errorMessage = String(e);
    } finally {
      sending = false;
      stopStreamingTimer();
      stopTypingEffect();
      if (componentActive && !$streamingStore[tabId]?.pendingQuestion) {
        streamingStore.clear(tabId);
      }
    }
  }

  async function handleSend() {
    if ((!inputMessage.trim() && attachedFiles.length === 0) || sending) return;

    const trimmed = inputMessage.trim();

    // Intercept slash commands
    if (trimmed.startsWith('/')) {
      const handled = await handleSlashCommand(trimmed);
      if (handled) return;
    }

    errorMessage = ''; // Clear previous error

    // Clear stale streaming state before starting new send
    stopTypingEffect();
    streamingStore.clear(tabId);
    cliPanel = null;  // Clear any existing AskUserQuestion panel

    sending = true;
    startStreamingTimer();
    autoScrollEnabled = true; // Reset scroll lock when sending new message

    // Separate files with line ranges (snippets) from full files
    const fullFiles = attachedFiles.filter(f => !f.lineRange);
    const snippetFiles = attachedFiles.filter(f => f.lineRange);

    // Build snippet prefix from files with line ranges
    let snippetPrefix = '';
    for (const sf of snippetFiles) {
      try {
        const parts = sf.lineRange.split('-').map(Number);
        const start = parts[0] || 1;
        const end = parts[1] || start;
        const result = await ReadFileSnippet(sf.path, start, end);
        const lang = result.language || '';
        snippetPrefix += `\`\`\`${lang}\n// ${result.fileName}:${sf.lineRange}\n${result.content}\n\`\`\`\n\n`;
      } catch (err) {
        console.warn('Failed to read snippet:', sf.path, err);
      }
    }

    const messageToSend = snippetPrefix + inputMessage;
    const filesToSend = fullFiles.map(f => f.path);

    // Cache image previews before clearing attachments
    cacheAttachedPreviews();

    console.log('[ConversationTab] Sending message:', { tabId, message: messageToSend, files: filesToSend, adminMode, teamsMode });

    // 히스토리에 저장 (빈 메시지, 슬래시 명령은 제외 — 이미 early return됨)
    if (trimmed) {
      if (inputHistory.length === 0 || inputHistory[inputHistory.length - 1] !== trimmed) {
        inputHistory = [...inputHistory, trimmed];
      }
    }
    historyIndex = -1;
    historySavedInput = '';

    inputMessage = ''; // Clear input immediately for better UX
    attachedFiles = []; // Clear attachments

    // Immediately add user message to display so it appears right away
    const userMsg = { role: 'user', content: messageToSend };
    if (filesToSend.length > 0) {
      userMsg.attachments = filesToSend;
    }
    displayMessages = [...displayMessages, userMsg];
    lastMessagesLength = displayMessages.length;
    setTimeout(() => scrollToBottom(), 0);

    try {
      if (teamsMode) {
        // Teams (Beta) mode: single CLI call with --agents
        console.log('[ConversationTab] Calling SendTeamsMessage (teams)...');
        await SendTeamsMessage(tabId, messageToSend, filesToSend);
        console.log('[ConversationTab] Teams message completed');
      } else if (adminMode) {
        // Admin mode: use orchestration flow
        console.log('[ConversationTab] Calling SendAdminMessage (orchestration)...');
        await SendAdminMessage(tabId, messageToSend, filesToSend);
        console.log('[ConversationTab] Admin message orchestration completed');
      } else {
        console.log('[ConversationTab] Calling SendMessage...');
        await SendMessage(tabId, messageToSend, filesToSend);
        console.log('[ConversationTab] Message sent successfully');
      }
      errorMessage = ''; // Clear error on success
    } catch (error) {
      console.error('[ConversationTab] Failed to send message:', error);
      errorMessage = String(error);
    } finally {
      sending = false;
      stopStreamingTimer();
      if (planMode) planResponseReceived = true;
      autoScrollEnabled = true; // Always restore after response completes
      console.log('[ConversationTab] sending set to false');
      stopTypingEffect();
      // Only clear store if this component is still alive
      // (if destroyed, the new component may be using the store data)
      if (componentActive && !$streamingStore[tabId]?.pendingQuestion) {
        streamingStore.clear(tabId);
      }
    }
  }

  async function handleCancel() {
    try {
      if (teamsMode) {
        await CancelOrchestrationJob(tabId);
        console.log('[ConversationTab] Teams orchestration cancelled');
      } else if (adminMode) {
        await CancelOrchestrationJob(tabId);
        console.log('[ConversationTab] Orchestration cancelled');
      } else {
        await CancelMessage(tabId);
        console.log('[ConversationTab] Message cancelled');
      }
    } catch (error) {
      console.error('[ConversationTab] Failed to cancel:', error);
    }
  }

  async function handleRetryMessage(messageIndex) {
    if (sending) return;
    const msg = displayMessages[messageIndex];
    if (!msg || msg.role !== 'user') return;

    const retryContent = msg.content;
    const retryFiles = msg.attachments || [];

    // Truncate backend conversation from this message onward
    try {
      await TruncateMessages(tabId, messageIndex);
    } catch (e) {
      console.warn('[ConversationTab] Failed to truncate messages:', e);
    }

    // Truncate local display
    displayMessages = displayMessages.slice(0, messageIndex);
    lastMessagesLength = displayMessages.length;

    // Set up input and send
    inputMessage = retryContent;
    attachedFiles = retryFiles.map(path => ({ path, name: path.split('/').pop() }));
    attachedFiles = attachedFiles;
    await handleSend();
  }

  async function handleFileSelect() {
    try {
      const files = await SelectFiles();
      if (files && files.length > 0) {
        for (const filePath of files) {
          await addFile(filePath);
        }
      }
    } catch (error) {
      console.error('Failed to select files:', error);
    }
  }

  async function addFile(filePath) {
    // Prevent duplicate files
    if (attachedFiles.some(f => f.path === filePath)) {
      return;
    }

    try {
      const fileInfo = await GetFileInfo(filePath);

      // Create preview for images
      let preview = null;
      if (fileInfo.mimeType.startsWith('image/')) {
        preview = `data:${fileInfo.mimeType};base64,${fileInfo.data}`;
      }

      attachedFiles = [...attachedFiles, {
        path: filePath,
        name: fileInfo.name,
        size: fileInfo.size,
        mimeType: fileInfo.mimeType,
        preview: preview
      }];
    } catch (error) {
      console.error('Failed to add file:', error);
      alert('파일 추가 실패: ' + error);
    }
  }

  function removeFile(index) {
    attachedFiles = attachedFiles.filter((_, i) => i !== index);
  }

  function handleDragOver(event) {
    event.preventDefault();
    dragOver = true;
  }

  function handleDragLeave(event) {
    event.preventDefault();
    dragOver = false;
  }

  async function handleDrop(event) {
    event.preventDefault();
    dragOver = false;

    const files = event.dataTransfer.files;
    if (!files || files.length === 0) return;

    for (const file of files) {
      try {
        // Read file as base64 via FileReader
        const base64Data = await readFileAsBase64(file);
        // Save to temp file on backend to get absolute path
        const savedPath = await SaveDroppedFile(file.name, base64Data);
        // Add file using the saved path
        await addFile(savedPath);
      } catch (err) {
        console.error('Failed to process dropped file:', err);
        errorMessage = `파일 처리 실패: ${file.name}`;
      }
    }
  }

  function readFileAsBase64(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        // result is "data:image/png;base64,AAAA..." - extract just the base64 part
        const result = reader.result;
        const base64 = result.split(',')[1];
        resolve(base64);
      };
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
  }

  function isImagePath(path) {
    if (!path) return false;
    const ext = path.split('.').pop().toLowerCase();
    return ['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext);
  }

  function shortenPath(p) {
    if (!p) return '';
    const parts = p.split('/');
    if (parts.length > 3 && p.startsWith('/Users/')) {
      return '~/' + parts.slice(3).join('/');
    }
    return p;
  }

  function openLightbox(src) {
    lightboxSrc = src;
  }

  function closeLightbox() {
    lightboxSrc = null;
  }

  // Cache attached file previews before sending so they're available for bubble display
  function cacheAttachedPreviews() {
    for (const file of attachedFiles) {
      if (file.preview && file.path) {
        imagePreviewCache[file.path] = file.preview;
      }
    }
    imagePreviewCache = imagePreviewCache; // trigger reactivity
  }

  // Svelte action: lazy-load image preview for a given path
  function loadImagePreview(node, path) {
    if (imagePreviewCache[path]) return { destroy() {} };

    GetFileInfo(path).then(info => {
      if (info.mimeType && info.mimeType.startsWith('image/')) {
        imagePreviewCache[path] = `data:${info.mimeType};base64,${info.data}`;
        imagePreviewCache = { ...imagePreviewCache };
      }
    }).catch(err => {
      console.warn('Failed to load image preview:', path, err);
    });

    return { destroy() {} };
  }

  async function requestCodeReview() {
    try {
      await RequestCodeReview(tabId);
    } catch (e) {
      console.error('Code review failed:', e);
    }
  }

  async function toggleAdminMode() {
    try {
      await ToggleTabAdminMode(tabId, adminMode);
      // If enabling admin mode, disable teams mode
      if (adminMode) teamsMode = false;
      // Refresh parent to update all tabs
      dispatch('refresh');
    } catch (error) {
      console.error('Failed to toggle admin mode:', error);
      // Revert on error
      adminMode = !adminMode;
    }
  }

  async function toggleTeamsMode() {
    try {
      await ToggleTabTeamsMode(tabId, teamsMode);
      // If enabling teams mode, disable admin mode
      if (teamsMode) adminMode = false;
      dispatch('refresh');
    } catch (error) {
      console.error('Failed to toggle teams mode:', error);
      teamsMode = !teamsMode;
    }
  }

  async function togglePlanMode() {
    try {
      await ToggleTabPlanMode(tabId, planMode);
      if (planMode) planResponseReceived = false;
      dispatch('refresh');
    } catch (error) {
      console.error('Failed to toggle plan mode:', error);
      planMode = !planMode;
    }
  }

  async function executePlan() {
    closeCliPanel();
    planMode = false;
    planResponseReceived = false;
    await ToggleTabPlanMode(tabId, false);
    dispatch('refresh');
    inputMessage = '위 플랜을 승인합니다. 구현을 시작해주세요. 각 단계를 빠짐없이 실행하고, 완료 후 결과를 요약해 주세요.';
    await handleSend();
  }

  async function viewPlanFile() {
    try {
      const content = await GetPlanContent(tabId);
      if (!content) {
        showCliResult('이 세션에 플랜 파일이 아직 없습니다. Plan 모드에서 응답을 받은 후 다시 시도해주세요.');
        return;
      }
      cliPanel = { type: 'plan-viewer', content };
    } catch (error) {
      showCliResult(`플랜 파일 로드 실패: ${error}`);
    }
  }

  async function refreshPlanContent() {
    try {
      const content = await GetPlanContent(tabId);
      if (!content) {
        showCliResult('이 세션에 플랜 파일이 아직 없습니다. Plan 모드에서 응답을 받은 후 다시 시도해주세요.');
        return;
      }
      cliPanel = { type: 'plan-viewer', content };
    } catch (error) {
      showCliResult(`플랜 파일 로드 실패: ${error}`);
    }
  }

  function handleInput() {
    // 히스토리 탐색 중 직접 타이핑하면 탐색 모드 해제
    if (historyIndex !== -1) {
      historyIndex = -1;
      historySavedInput = '';
    }
    const trimmed = inputMessage.trim();
    if (trimmed.startsWith('/') && trimmed.length > 0) {
      const prefix = trimmed.substring(1).split(/\s/)[0];
      popupCommands = prefix ? matchCommands(prefix) : getAllCommands();
      showPopup = popupCommands.length > 0;
      popupSelectedIndex = 0;
      // Close file popup when slash command is active
      showFilePopup = false;
      filePopupItems = [];
    } else {
      showPopup = false;
      popupCommands = [];

      // @ file path detection
      if (textareaEl) {
        const cursorPos = textareaEl.selectionStart;
        const textBeforeCursor = inputMessage.substring(0, cursorPos);
        const atIdx = textBeforeCursor.lastIndexOf('@');

        if (atIdx >= 0 && (atIdx === 0 || textBeforeCursor[atIdx - 1] === ' ' || textBeforeCursor[atIdx - 1] === '\n')) {
          const partial = textBeforeCursor.substring(atIdx + 1);
          atTokenStart = atIdx;
          fetchFileCompletions(partial);
        } else {
          showFilePopup = false;
          filePopupItems = [];
        }
      }
    }
  }

  function handlePopupSelect(event) {
    const cmd = event.detail;
    inputMessage = `/${cmd.name} `;
    showPopup = false;
    popupCommands = [];
  }

  async function fetchFileCompletions(partial) {
    try {
      const baseDir = (workDir || '').replace(/\/$/, '');

      // Determine mode: browse (empty or ends with /) vs search (has query text)
      const isBrowse = !partial || partial.endsWith('/');

      if (isBrowse) {
        // Browse mode: list directory contents via CompletePath
        const searchPath = partial ? baseDir + '/' + partial : baseDir + '/';
        const results = await CompletePath(searchPath);

        if (results && results.length > 0) {
          filePopupItems = results.map(fullPath => {
            const isDir = fullPath.endsWith('/');
            const cleanPath = isDir ? fullPath.slice(0, -1) : fullPath;
            const name = cleanPath.split('/').filter(Boolean).pop() + (isDir ? '/' : '');
            const relativePath = fullPath.startsWith(baseDir)
              ? fullPath.substring(baseDir.length + 1)
              : fullPath;
            const ext = isDir ? '' : (name.split('.').pop() || '');
            return { name, fullPath, relativePath, isDir, ext };
          });
          showFilePopup = true;
          filePopupSelectedIndex = 0;
        } else {
          showFilePopup = false;
          filePopupItems = [];
        }
      } else {
        // Search mode: recursive search via SearchFiles
        // If partial contains '/', split into subdir + query
        const lastSlash = partial.lastIndexOf('/');
        let searchBase, query;
        if (lastSlash >= 0) {
          searchBase = baseDir + '/' + partial.substring(0, lastSlash);
          query = partial.substring(lastSlash + 1);
        } else {
          searchBase = baseDir;
          query = partial;
        }

        if (!query) {
          // Empty query after slash — fall back to browse
          const searchPath = searchBase + '/';
          const results = await CompletePath(searchPath);
          if (results && results.length > 0) {
            filePopupItems = results.map(fullPath => {
              const isDir = fullPath.endsWith('/');
              const cleanPath = isDir ? fullPath.slice(0, -1) : fullPath;
              const name = cleanPath.split('/').filter(Boolean).pop() + (isDir ? '/' : '');
              const relativePath = fullPath.startsWith(baseDir)
                ? fullPath.substring(baseDir.length + 1)
                : fullPath;
              const ext = isDir ? '' : (name.split('.').pop() || '');
              return { name, fullPath, relativePath, isDir, ext };
            });
            showFilePopup = true;
            filePopupSelectedIndex = 0;
          } else {
            showFilePopup = false;
            filePopupItems = [];
          }
          return;
        }

        const results = await SearchFiles(searchBase, query);

        if (results && results.length > 0) {
          filePopupItems = results.map(fullPath => {
            const isDir = fullPath.endsWith('/');
            const cleanPath = isDir ? fullPath.slice(0, -1) : fullPath;
            const name = cleanPath.split('/').filter(Boolean).pop() + (isDir ? '/' : '');
            const relativePath = fullPath.startsWith(baseDir)
              ? fullPath.substring(baseDir.length + 1)
              : fullPath;
            const ext = isDir ? '' : (name.split('.').pop() || '');
            return { name, fullPath, relativePath, isDir, ext };
          });
          showFilePopup = true;
          filePopupSelectedIndex = 0;
        } else {
          showFilePopup = false;
          filePopupItems = [];
        }
      }
    } catch {
      showFilePopup = false;
      filePopupItems = [];
    }
  }

  function selectFilePath(item) {
    const before = inputMessage.substring(0, atTokenStart);
    const afterCursor = inputMessage.substring(textareaEl.selectionStart);
    const cleanPath = item.fullPath.endsWith('/') ? item.fullPath.slice(0, -1) : item.fullPath;
    inputMessage = before + cleanPath + ' ' + afterCursor.trimStart();
    showFilePopup = false;
    filePopupItems = [];
    tick().then(() => {
      const newPos = before.length + cleanPath.length + 1;
      textareaEl.setSelectionRange(newPos, newPos);
      textareaEl.focus();
    });
  }

  function navigateFileDir(item) {
    const before = inputMessage.substring(0, atTokenStart + 1);
    const baseDir = (workDir || '').replace(/\/$/, '');
    const relativePath = item.fullPath.startsWith(baseDir + '/')
      ? item.fullPath.substring(baseDir.length + 1)
      : item.fullPath;
    const afterCursor = inputMessage.substring(textareaEl.selectionStart);
    inputMessage = before + relativePath + afterCursor.trimStart();
    tick().then(() => {
      const newPos = (before + relativePath).length;
      textareaEl.setSelectionRange(newPos, newPos);
      textareaEl.focus();
    });
    fetchFileCompletions(relativePath);
  }

  function handleKeydown(event) {
    if (showFilePopup && filePopupItems.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        filePopupSelectedIndex = (filePopupSelectedIndex + 1) % filePopupItems.length;
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        filePopupSelectedIndex = (filePopupSelectedIndex - 1 + filePopupItems.length) % filePopupItems.length;
        return;
      }
      if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
        event.preventDefault();
        event.stopPropagation();
        const item = filePopupItems[filePopupSelectedIndex];
        if (item.isDir) {
          navigateFileDir(item);
        } else {
          selectFilePath(item);
        }
        return;
      }
      if (event.key === 'Tab') {
        event.preventDefault();
        const item = filePopupItems[filePopupSelectedIndex];
        if (item.isDir) {
          navigateFileDir(item);
        } else {
          selectFilePath(item);
        }
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        showFilePopup = false;
        filePopupItems = [];
        return;
      }
    }

    if (showPopup) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        popupSelectedIndex = (popupSelectedIndex + 1) % popupCommands.length;
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        popupSelectedIndex = (popupSelectedIndex - 1 + popupCommands.length) % popupCommands.length;
        return;
      }
      if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
        event.preventDefault();
        event.stopPropagation();
        const cmd = popupCommands[popupSelectedIndex];
        inputMessage = `/${cmd.name} `;
        showPopup = false;
        popupCommands = [];
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        showPopup = false;
        popupCommands = [];
        return;
      }
      if (event.key === 'Tab') {
        event.preventDefault();
        const cmd = popupCommands[popupSelectedIndex];
        inputMessage = `/${cmd.name} `;
        showPopup = false;
        popupCommands = [];
        return;
      }
    }

    // Input history navigation (ArrowUp/Down on empty textarea)
    if (event.key === 'ArrowUp' && !showPopup && !showFilePopup && inputHistory.length > 0) {
      const cursorPos = textareaEl.selectionStart;
      const textBeforeCursor = inputMessage.substring(0, cursorPos);
      if (textBeforeCursor.includes('\n')) return;

      if (historyIndex === -1) {
        historySavedInput = inputMessage;
        historyIndex = inputHistory.length - 1;
      } else if (historyIndex > 0) {
        historyIndex--;
      }
      event.preventDefault();
      inputMessage = inputHistory[historyIndex];
      tick().then(() => textareaEl.setSelectionRange(inputMessage.length, inputMessage.length));
      return;
    }

    if (event.key === 'ArrowDown' && !showPopup && !showFilePopup && historyIndex !== -1) {
      const cursorPos = textareaEl.selectionStart;
      const textAfterCursor = inputMessage.substring(cursorPos);
      if (textAfterCursor.includes('\n')) return;

      event.preventDefault();
      if (historyIndex < inputHistory.length - 1) {
        historyIndex++;
        inputMessage = inputHistory[historyIndex];
      } else {
        historyIndex = -1;
        inputMessage = historySavedInput;
        historySavedInput = '';
      }
      tick().then(() => textareaEl.setSelectionRange(inputMessage.length, inputMessage.length));
      return;
    }
  }

  // --- Search functions ---
  function openSearch() {
    searchOpen = true;
    tick().then(() => searchInputEl?.focus());
  }

  function closeSearch() {
    searchOpen = false;
    searchQuery = '';
    searchMatches = [];
    searchCurrentIndex = -1;
    clearSearchHighlights();
  }

  function clearSearchHighlights() {
    if (!messagesArea) return;
    const marks = messagesArea.querySelectorAll('mark.search-highlight');
    marks.forEach(mark => {
      const parent = mark.parentNode;
      parent.replaceChild(document.createTextNode(mark.textContent), mark);
      parent.normalize();
    });
  }

  function performSearch() {
    clearSearchHighlights();
    searchMatches = [];
    searchCurrentIndex = -1;
    if (!searchQuery || !messagesArea) return;

    const query = searchQuery.toLowerCase();
    const walker = document.createTreeWalker(messagesArea, NodeFilter.SHOW_TEXT, null);
    const textNodes = [];
    while (walker.nextNode()) textNodes.push(walker.currentNode);

    for (const node of textNodes) {
      const text = node.textContent;
      const lower = text.toLowerCase();
      let idx = 0;
      const parts = [];
      let lastEnd = 0;

      while ((idx = lower.indexOf(query, idx)) !== -1) {
        if (idx > lastEnd) parts.push({ text: text.substring(lastEnd, idx), match: false });
        parts.push({ text: text.substring(idx, idx + query.length), match: true });
        lastEnd = idx + query.length;
        idx = lastEnd;
      }

      if (parts.length === 0) continue;
      if (lastEnd < text.length) parts.push({ text: text.substring(lastEnd), match: false });

      const frag = document.createDocumentFragment();
      for (const p of parts) {
        if (p.match) {
          const mark = document.createElement('mark');
          mark.className = 'search-highlight';
          mark.textContent = p.text;
          searchMatches.push(mark);
          frag.appendChild(mark);
        } else {
          frag.appendChild(document.createTextNode(p.text));
        }
      }
      node.parentNode.replaceChild(frag, node);
    }

    searchMatches = searchMatches;
    if (searchMatches.length > 0) {
      searchCurrentIndex = 0;
      highlightCurrentMatch();
    }
  }

  function highlightCurrentMatch() {
    searchMatches.forEach(m => m.classList.remove('search-current'));
    if (searchCurrentIndex >= 0 && searchCurrentIndex < searchMatches.length) {
      const el = searchMatches[searchCurrentIndex];
      el.classList.add('search-current');
      el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  }

  function searchNext() {
    if (searchMatches.length === 0) return;
    searchCurrentIndex = (searchCurrentIndex + 1) % searchMatches.length;
    highlightCurrentMatch();
  }

  function searchPrev() {
    if (searchMatches.length === 0) return;
    searchCurrentIndex = (searchCurrentIndex - 1 + searchMatches.length) % searchMatches.length;
    highlightCurrentMatch();
  }

  function handleSearchKeydown(event) {
    if (event.key === 'Escape') { event.preventDefault(); closeSearch(); return; }
    if (event.key === 'Enter' && event.shiftKey) { event.preventDefault(); searchPrev(); return; }
    if (event.key === 'Enter') { event.preventDefault(); searchNext(); return; }
  }

  // --- Scroll to message top ---
  function scrollToMessageTop(event) {
    const messageEl = event.currentTarget.closest('.message');
    if (messageEl && messagesArea) {
      messagesArea.scrollTo({ top: messageEl.offsetTop - 10, behavior: 'smooth' });
    }
  }

  // --- Svelte action: track message height for long-message class ---
  let messageTall = {};

  function trackMessageHeight(node, index) {
    function check() {
      const tall = node.scrollHeight > 400;
      if (messageTall[index] !== tall) {
        messageTall[index] = tall;
        messageTall = messageTall; // trigger reactivity
      }
    }
    const timer = setTimeout(check, 100);
    const observer = new ResizeObserver(check);
    observer.observe(node);
    return { destroy() { clearTimeout(timer); observer.disconnect(); } };
  }

  function handleWindowKeydown(event) {
    // Only handle keyboard shortcuts if this tab has focus
    if ($focusedTabId !== tabId) return;
    // In-page search shortcut (Cmd/Ctrl+F)
    if (event.key === 'f' && (isMac ? event.metaKey : event.ctrlKey) && !event.shiftKey) {
      event.preventDefault();
      searchOpen ? searchInputEl?.focus() : openSearch();
      return;
    }
    if (searchOpen && event.key === 'Escape') {
      event.preventDefault();
      closeSearch();
      return;
    }
    if (lightboxSrc && event.key === 'Escape') {
      event.preventDefault();
      closeLightbox();
      return;
    }

    // CLI panel keyboard handling (highest priority)
    if (cliPanel) {
      // Plan execution shortcut (Cmd/Ctrl+Shift+Enter)
      if (cliPanel.type === 'plan-ready' && event.key === 'Enter' && event.shiftKey && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        executePlan();
        return;
      }
      if (cliPanel.type === 'ask-user-question' && cliPanel.showCustomInput) {
        // Custom text input mode: only handle Escape to cancel
        if (event.key === 'Escape') {
          event.preventDefault();
          cliPanel.showCustomInput = false;
          cliPanel = cliPanel;
          return;
        }
        // Let all other keys (typing, Enter handled by input's on:keydown) pass through to the input
        return;
      }
      if (cliPanel.type === 'ask-user-question' && !cliPanel.reviewing && !cliPanel.showCustomInput) {
        const q = cliPanel.questions[cliPanel.currentStep];
        const totalItems = q.options.length + 1; // +1 for "Other"
        if (event.key === 'ArrowDown') {
          event.preventDefault();
          cliPanel.selectedIndex = (cliPanel.selectedIndex + 1) % totalItems;
          cliPanel = cliPanel;
          return;
        }
        if (event.key === 'ArrowUp') {
          event.preventDefault();
          cliPanel.selectedIndex = (cliPanel.selectedIndex - 1 + totalItems) % totalItems;
          cliPanel = cliPanel;
          return;
        }
        if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
          event.preventDefault();
          if (cliPanel.selectedIndex === q.options.length) {
            pickAskCustom();
          } else {
            pickAskOption(cliPanel.selectedIndex);
          }
          return;
        }
        if (event.key === 'ArrowLeft' && cliPanel.currentStep > 0) {
          event.preventDefault();
          cliPanel.currentStep--;
          cliPanel.selectedIndex = 0;
          cliPanel = cliPanel;
          return;
        }
        if (event.key === 'ArrowRight' && cliPanel.currentStep < cliPanel.questions.length - 1 && cliPanel.answers[q.question]) {
          event.preventDefault();
          cliPanel.currentStep++;
          cliPanel.selectedIndex = 0;
          cliPanel = cliPanel;
          return;
        }
        if (event.key === 'Escape') {
          event.preventDefault();
          closeCliPanel();
          return;
        }
        return; // consume all other keys while ask panel is open
      }
      if (cliPanel.type === 'ask-user-question' && cliPanel.reviewing) {
        if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
          event.preventDefault();
          submitAskAnswers();
          return;
        }
        if (event.key === 'Escape') {
          event.preventDefault();
          cliPanel.reviewing = false;
          cliPanel = cliPanel;
          return;
        }
        return;
      }
      if (cliPanel.type === 'model-select' || cliPanel.type === 'choice') {
        const items = cliPanel.type === 'model-select' ? cliPanel.models : cliPanel.choices;
        const totalItems = cliPanel.type === 'choice' ? items.length + 1 : items.length; // +1 for "Other"

        // 커스텀 입력 중이면 키보드를 입력 필드에 위임
        if (cliPanel.type === 'choice' && cliPanel.showCustomInput) {
          if (event.key === 'Escape') {
            event.preventDefault();
            cliPanel.showCustomInput = false;
            cliPanel = cliPanel;
            return;
          }
          return; // 나머지 키는 input에 위임
        }

        // textarea에 포커스가 있으면 방향키는 textarea가 처리 (히스토리 탐색 등)
        const textareaFocused = document.activeElement === textareaEl;
        if (event.key === 'ArrowDown' && !textareaFocused) {
          event.preventDefault();
          cliPanelSelectedIndex = (cliPanelSelectedIndex + 1) % totalItems;
          return;
        }
        if (event.key === 'ArrowUp' && !textareaFocused) {
          event.preventDefault();
          cliPanelSelectedIndex = (cliPanelSelectedIndex - 1 + totalItems) % totalItems;
          return;
        }
        if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) {
          event.preventDefault();
          // If textarea has focus and content, send message instead of selecting choice
          if (document.activeElement === textareaEl && inputMessage.trim()) {
            handleSend();
            return;
          }
          if (cliPanel.type === 'model-select') {
            selectModel();
          } else if (cliPanel.type === 'choice') {
            selectChoice();
          }
          return;
        }
        if (event.key === 'Escape') {
          event.preventDefault();
          closeCliPanel();
          return;
        }
      }
      // result / plan-viewer type: Esc dismisses
      if (cliPanel.type === 'result' || cliPanel.type === 'plan-viewer') {
        if (event.key === 'Escape') {
          event.preventDefault();
          closeCliPanel();
          return;
        }
      }
    }
    // Shift+Tab: toggle plan mode
    if (event.key === 'Tab' && event.shiftKey) {
      event.preventDefault();
      planMode = !planMode;
      togglePlanMode();
      return;
    }
    // Cmd/Ctrl+Enter: send message
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      handleSend();
    }
  }

  onDestroy(() => {
    componentActive = false;
    // Only clean up timers — don't reset typingContent/fullContent
    // (component is being destroyed so variable reset is unnecessary,
    //  and stopTypingEffect() would clear state the new component needs)
    if (typingTimer) { clearInterval(typingTimer); typingTimer = null; }
    stopStreamingTimer();
    if (placeholderTimer) clearInterval(placeholderTimer);
    if (cliPanelTimer) clearTimeout(cliPanelTimer);
    if (copiedTimer) clearTimeout(copiedTimer);
  });
</script>

<svelte:window on:mousemove={handleInputResizeMove} on:mouseup={stopInputResize} on:keydown={handleWindowKeydown} />

<div class="conversation-tab"
     bind:this={conversationTabEl}
     class:drag-over={dragOver}
     on:focusin={() => focusedTabId.set(tabId)}
     on:mousedown={() => focusedTabId.set(tabId)}
     on:dragover={handleDragOver}
     on:dragleave={handleDragLeave}
     on:drop={handleDrop}>
  {#if searchOpen}
    <div class="search-bar">
      <input bind:this={searchInputEl} bind:value={searchQuery}
             on:input={performSearch} on:keydown={handleSearchKeydown}
             placeholder="검색..." spellcheck="false" />
      <span class="search-count">
        {#if searchMatches.length > 0}{searchCurrentIndex + 1}/{searchMatches.length}
        {:else if searchQuery}0건{/if}
      </span>
      <button class="search-nav-btn" on:click={searchPrev} title="이전 (Shift+Enter)">&#9650;</button>
      <button class="search-nav-btn" on:click={searchNext} title="다음 (Enter)">&#9660;</button>
      <button class="search-close-btn" on:click={closeSearch} title="닫기 (Esc)">&#10005;</button>
    </div>
  {/if}
  <div class="messages-area" bind:this={messagesArea} on:scroll={handleMessagesScroll}>
    {#if displayMessages.length === 0 && !sending}
      <div class="empty-state">
        <p>대화를 시작하세요.</p>
      </div>
    {:else}
      {#each displayMessages as message, index (index)}
        {#if message.role === 'divider'}
          <div class="clear-divider">
            <span class="clear-divider-line"></span>
            <span class="clear-divider-label">대화 초기화됨</span>
            <span class="clear-divider-line"></span>
          </div>
        {:else}
          <div class="message" class:user={message.role === 'user'} class:assistant={message.role === 'assistant'} class:system={message.role === 'system'} class:completed={message.role === 'assistant'} class:long-message={messageTall[index]} use:trackMessageHeight={index}>
            {#if message.role === 'user' && message.attachments && message.attachments.length > 0}
              <div class="message-attachments">
                {#each message.attachments as att}
                  {#if isImagePath(att)}
                    <div class="message-image-item">
                      {#if imagePreviewCache[att]}
                        <img src={imagePreviewCache[att]} alt={att.split('/').pop()} class="bubble-image-thumb" on:click={() => openLightbox(imagePreviewCache[att])} />
                      {:else}
                        <div class="image-placeholder" use:loadImagePreview={att}>
                          {att.split('/').pop()}
                        </div>
                      {/if}
                      <span class="image-path-label">{shortenPath(att)}</span>
                    </div>
                  {:else}
                    <div class="code-attachment">
                      <span class="code-att-ext">{att.split('.').pop()}</span>
                      <span class="code-att-path">{shortenPath(att)}</span>
                    </div>
                  {/if}
                {/each}
              </div>
            {/if}
            <div class="message-content">
              <MarkdownViewer content={message.content} />
            </div>
            {#if message.role === 'assistant' && (message.durationMs || message.inputTokens)}
              <span class="msg-duration">
                {#if message.durationMs}{formatDuration(message.durationMs)}{/if}
                {#if message.inputTokens}
                  <span class="token-in" title="Input tokens">↑{formatTokens(message.inputTokens)}</span>
                  <span class="token-out" title="Output tokens">↓{formatTokens(message.outputTokens)}</span>
                {/if}
              </span>
            {/if}
            {#if message.toolUses && message.toolUses.length > 0}
              <button class="process-toggle-btn" on:click={() => toggleProcess(index)}>
                ⚙️ 과정 보기 ({message.toolUses.length})
                <span class="chevron" class:open={expandedProcessIndex === index}>▸</span>
              </button>
              {#if expandedProcessIndex === index}
                <div class="process-details">
                  {#each message.toolUses as tu, i}
                    <div class="process-step">
                      <span class="step-num">{i + 1}</span>
                      <span class="step-icon">{getToolIcon(tu.toolName)}</span>
                      <span class="step-name">{tu.toolName}</span>
                      <span class="step-detail">{tu.detail}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            {/if}
            {#if message.role === 'assistant'}
              <button class="msg-expand-btn" on:click={() => openExpandedView(message.content)} title="확대 보기">
                <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M1.5 1h4a.5.5 0 010 1H2.707l3.147 3.146a.5.5 0 11-.708.708L2 2.707V5.5a.5.5 0 01-1 0v-4a.5.5 0 01.5-.5zm13 0a.5.5 0 01.5.5v4a.5.5 0 01-1 0V2.707l-3.146 3.147a.5.5 0 11-.708-.708L13.293 2H10.5a.5.5 0 010-1h4zm-13 14a.5.5 0 01-.5-.5v-4a.5.5 0 011 0v2.793l3.146-3.147a.5.5 0 11.708.708L2.707 14H5.5a.5.5 0 010 1h-4zm13 0h-4a.5.5 0 010-1h2.793l-3.147-3.146a.5.5 0 11.708-.708L14 13.293V10.5a.5.5 0 011 0v4a.5.5 0 01-.5.5z"/></svg>
              </button>
              <button class="msg-scroll-top-btn" on:click={scrollToMessageTop} title="메시지 상단으로">
                <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><rect x="3" y="1" width="10" height="2" rx="1"/><path d="M8 5a.5.5 0 01.354.146l4 4a.5.5 0 01-.708.708L8.5 6.707V14.5a.5.5 0 01-1 0V6.707L4.354 9.854a.5.5 0 11-.708-.708l4-4A.5.5 0 018 5z"/></svg>
              </button>
            {/if}
            {#if message.role === 'user'}
              <button class="msg-copy-btn" on:click={() => copyMessageContent(message.content, index)} title="메시지 복사">
                {#if copiedIndex === index}
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M13.78 4.22a.75.75 0 010 1.06l-7.25 7.25a.75.75 0 01-1.06 0L2.22 9.28a.75.75 0 011.06-1.06L6 10.94l6.72-6.72a.75.75 0 011.06 0z"/></svg>
                {:else}
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 010 1.5h-1.5a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-1.5a.75.75 0 011.5 0v1.5A1.75 1.75 0 019.25 16h-7.5A1.75 1.75 0 010 14.25zM5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0114.25 11h-7.5A1.75 1.75 0 015 9.25zm1.75-.25a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-7.5a.25.25 0 00-.25-.25z"/></svg>
                {/if}
              </button>
              {#if !sending}
                <button class="msg-retry-btn" on:click={() => handleRetryMessage(index)} title="이 메시지부터 다시 보내기">
                  ↻
                </button>
              {/if}
            {/if}
          </div>
        {/if}
      {/each}
      {#if sending}
        <div class="message assistant streaming">
          <div class="message-content">
            {#if typingContent}
              <div class="streaming-content-wrapper" class:overflowing={streamingOverflowing} bind:this={streamingWrapperEl}>
                <div class="streaming-content-fade"></div>
                <div class="streaming-content-body" bind:this={streamingBodyEl}>
                  <MarkdownViewer content={typingContent} />
                </div>
              </div>
              <div class="streaming-indicator">
                <span class="streaming-dot"></span>
                <span class="streaming-dot"></span>
                <span class="streaming-dot"></span>
                {#if toolActivity}
                  <span class="streaming-tool-icon">{getToolIcon(toolActivity.toolName)}</span>
                  <span class="streaming-label">{getToolLabel(toolActivity.toolName)} {toolActivity.detail}</span>
                {:else}
                  <span class="streaming-label">응답 생성 중...</span>
                {/if}
                <span class="streaming-elapsed">{formatElapsed(streamingElapsed)}</span>
                {#if tokenUsage}
                  <span class="streaming-tokens">
                    <span class="token-in" title="Input tokens">↑{formatTokens(tokenUsage.inputTokens)}</span>
                    <span class="token-out" title="Output tokens">↓{formatTokens(tokenUsage.outputTokens)}</span>
                  </span>
                {/if}
              </div>
            {:else}
              <div class="streaming-indicator waiting">
                <span class="streaming-dot"></span>
                <span class="streaming-dot"></span>
                <span class="streaming-dot"></span>
                {#if toolActivity}
                  <span class="streaming-tool-icon">{getToolIcon(toolActivity.toolName)}</span>
                  <span class="streaming-label">{getToolLabel(toolActivity.toolName)} {toolActivity.detail}</span>
                {:else}
                  <span class="streaming-label">응답 생성 중...</span>
                {/if}
                <span class="streaming-elapsed">{formatElapsed(streamingElapsed)}</span>
                {#if tokenUsage}
                  <span class="streaming-tokens">
                    <span class="token-in" title="Input tokens">↑{formatTokens(tokenUsage.inputTokens)}</span>
                    <span class="token-out" title="Output tokens">↓{formatTokens(tokenUsage.outputTokens)}</span>
                  </span>
                {/if}
              </div>
            {/if}
          </div>
          {#if typingContent}
            <button class="msg-expand-btn" on:click={() => openExpandedView(fullContent || typingContent)} title="확대 보기">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M1.5 1h4a.5.5 0 010 1H2.707l3.147 3.146a.5.5 0 11-.708.708L2 2.707V5.5a.5.5 0 01-1 0v-4a.5.5 0 01.5-.5zm13 0a.5.5 0 01.5.5v4a.5.5 0 01-1 0V2.707l-3.146 3.147a.5.5 0 11-.708-.708L13.293 2H10.5a.5.5 0 010-1h4zm-13 14a.5.5 0 01-.5-.5v-4a.5.5 0 011 0v2.793l3.146-3.147a.5.5 0 11.708.708L2.707 14H5.5a.5.5 0 010 1h-4zm13 0h-4a.5.5 0 010-1h2.793l-3.147-3.146a.5.5 0 11.708-.708L14 13.293V10.5a.5.5 0 011 0v4a.5.5 0 01-.5.5z"/></svg>
            </button>
          {/if}
        </div>
      {/if}
    {/if}
  </div>

  {#if errorMessage}
    <div class="error-banner">
      <span class="error-icon">⚠️</span>
      <span class="error-text">{errorMessage}</span>
      <button class="error-close" on:click={() => errorMessage = ''} title="닫기">✕</button>
    </div>
  {/if}

  {#if cliPanel}
    <div class="cli-panel" bind:this={cliPanelEl} tabindex="-1">
      {#if cliPanel.type === 'model-select'}
        <div class="cli-title">모델 선택</div>
        <div class="cli-options">
          {#each cliPanel.models as model, i}
            <div
              class="cli-option"
              class:selected={i === cliPanelSelectedIndex}
              on:click={() => { cliPanelSelectedIndex = i; selectModel(); }}
            >
              <span class="cli-cursor">{i === cliPanelSelectedIndex ? '❯' : ' '}</span>
              <span class="cli-model-name">{model}</span>
              {#if model === cliPanel.currentModel}
                <span class="cli-badge">현재</span>
              {/if}
            </div>
          {/each}
        </div>
        <div class="cli-hint">↑↓ 이동 · Enter 선택 · Esc 취소</div>
      {:else if cliPanel.type === 'choice'}
        <div class="cli-title">{cliPanel.question}</div>
        <div class="cli-options">
          {#each cliPanel.choices as choice, i}
            <div
              class="cli-option"
              class:selected={i === cliPanelSelectedIndex}
              on:click={() => { cliPanelSelectedIndex = i; selectChoice(); }}
            >
              <span class="cli-cursor">{i === cliPanelSelectedIndex ? '❯' : ' '}</span>
              <span class="cli-choice-num">{choice.num}</span>
              <span class="cli-choice-text"><MarkdownViewer content={choice.text} /></span>
            </div>
          {/each}
          <!-- Other (custom text) option -->
          <div class="cli-option"
               class:selected={cliPanelSelectedIndex === cliPanel.choices.length}
               on:click={() => { cliPanelSelectedIndex = cliPanel.choices.length; selectChoice(); }}>
            <span class="cli-cursor">{cliPanelSelectedIndex === cliPanel.choices.length ? '❯' : ' '}</span>
            <span class="cli-choice-num">✎</span>
            <span class="cli-choice-text">기타 (직접 입력)</span>
          </div>
          {#if cliPanel.showCustomInput}
            <div class="ask-custom-row">
              <input class="ask-custom-input cli-custom-input"
                     bind:value={cliPanel.customText}
                     on:keydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submitChoiceCustom(); } }}
                     placeholder="답변을 입력하세요..." />
              <button class="ask-custom-submit" on:click={submitChoiceCustom}>OK</button>
            </div>
          {/if}
        </div>
        <div class="cli-hint">↑↓ 이동 · Enter 선택 · Esc 취소</div>
      {:else if cliPanel.type === 'ask-user-question'}
        <div class="ask-panel">
          <div class="ask-progress">
            {#each cliPanel.questions as q, i}
              <span class="ask-step"
                    class:done={cliPanel.answers[q.question]}
                    class:current={i === cliPanel.currentStep}>
                {q.header || `Q${i+1}`}
              </span>
            {/each}
            <button class="ask-submit-tab"
                    disabled={!allAskQuestionsAnswered()}
                    on:click={submitAskAnswers}>
              Submit
            </button>
          </div>

          {#if !cliPanel.reviewing}
            {@const q = cliPanel.questions[cliPanel.currentStep]}
            <div class="ask-question-text">{q.question}</div>
            <div class="cli-options">
              {#each q.options as opt, i}
                <div class="cli-option"
                     class:selected={i === cliPanel.selectedIndex}
                     on:click={() => pickAskOption(i)}>
                  <span class="cli-cursor">{i === cliPanel.selectedIndex ? '❯' : ' '}</span>
                  <div class="ask-opt-content">
                    <span class="ask-opt-label">{opt.label}</span>
                    {#if opt.description}
                      <span class="ask-opt-desc">{opt.description}</span>
                    {/if}
                  </div>
                </div>
              {/each}
              <!-- Other (custom text) option -->
              <div class="cli-option"
                   class:selected={cliPanel.selectedIndex === q.options.length}
                   on:click={pickAskCustom}>
                <span class="cli-cursor">{cliPanel.selectedIndex === q.options.length ? '❯' : ' '}</span>
                <div class="ask-opt-content">
                  <span class="ask-opt-label">Other</span>
                  <span class="ask-opt-desc">Type a custom answer</span>
                </div>
              </div>
              {#if cliPanel.showCustomInput}
                <div class="ask-custom-row">
                  <input class="ask-custom-input"
                         bind:value={cliPanel.customText}
                         on:keydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submitCustomAnswer(); } }}
                         placeholder="Enter your answer..." />
                  <button class="ask-custom-submit" on:click={submitCustomAnswer}>OK</button>
                </div>
              {/if}
            </div>
            <div class="cli-hint">↑↓ 이동 · Enter 선택 · ← 이전 · → 다음</div>
          {:else}
            <div class="ask-review">
              {#each cliPanel.questions as q}
                <div class="ask-review-item">
                  <span class="ask-review-q">{q.question}</span>
                  <span class="ask-review-a">→ {cliPanel.answers[q.question]}</span>
                </div>
              {/each}
            </div>
            <div class="ask-actions">
              <button class="ask-submit-btn" on:click={submitAskAnswers}>Submit answers</button>
              <button class="ask-cancel-btn" on:click={() => { cliPanel.reviewing = false; cliPanel = cliPanel; }}>Back</button>
            </div>
          {/if}
        </div>
      {:else if cliPanel.type === 'result'}
        <div class="cli-result-content">
          <MarkdownViewer content={cliPanel.content} />
        </div>
      {:else if cliPanel.type === 'plan-ready'}
        <div class="cli-plan-ready">
          <span class="plan-ready-text">플랜이 준비되었습니다</span>
          <button class="plan-view-btn" on:click={viewPlanFile}>플랜 파일 보기</button>
          <button class="plan-execute-btn" on:click={executePlan}>
            플랜 실행 <span class="shortcut-hint">{isMac ? '⌘' : 'Ctrl'}⇧↵</span>
          </button>
          <span class="plan-ready-hint">또는 추가 요청을 입력하세요</span>
        </div>
      {:else if cliPanel.type === 'plan-viewer'}
        <div class="cli-plan-viewer">
          <div class="plan-viewer-header">
            <span class="plan-viewer-title">Plan File</span>
            <button class="plan-viewer-btn" on:click={refreshPlanContent}>↻</button>
            <button class="plan-viewer-btn" on:click={() => OpenExpandedView(cliPanel.content)}>브라우저로 보기</button>
            <button class="plan-viewer-btn" on:click={closeCliPanel}>✕</button>
          </div>
          <div class="plan-viewer-content">
            <MarkdownViewer content={cliPanel.content} />
          </div>
        </div>
      {/if}
    </div>
  {/if}

  {#if (adminMode || teamsMode) && orchestratorState && Object.keys(orchestratorState.tasks).length > 0}
    <div class="orchestrator-dashboard">
      <div class="dashboard-title">{teamsMode ? 'Teams 에이전트 상태' : '워커 상태'}</div>
      <div class="dashboard-tasks">
        {#each Object.entries(orchestratorState.tasks) as [taskId, task]}
          <div class="task-card" class:running={task.status === 'running'} class:completed={task.status === 'completed'} class:failed={task.status === 'failed'}>
            <span class="task-status-icon">
              {#if task.status === 'running'}
                <span class="task-spinner"></span>
              {:else if task.status === 'completed'}
                ✓
              {:else if task.status === 'failed'}
                ✗
              {:else}
                ○
              {/if}
            </span>
            <span class="task-worker">{task.workerTabId}</span>
            <span class="task-desc">{task.description}</span>
          </div>
        {/each}
      </div>
      {#if orchestratorState.jobComplete}
        <div class="dashboard-complete">오케스트레이션 완료</div>
      {/if}
    </div>
  {/if}

  <div class="input-splitter" class:active={inputResizing} on:mousedown={startInputResize}></div>

  <div class="input-area" style="height: {inputAreaHeight}px;">
    {#if showPopup}
      <SlashCommandPopup
        commands={popupCommands}
        bind:selectedIndex={popupSelectedIndex}
        on:select={handlePopupSelect}
      />
    {/if}

    {#if showFilePopup}
      <FilePathPopup
        items={filePopupItems}
        bind:selectedIndex={filePopupSelectedIndex}
        on:select={(e) => selectFilePath(e.detail)}
        on:navigate={(e) => navigateFileDir(e.detail)}
      />
    {/if}

    <div class="input-controls">
      <label class="admin-mode-toggle">
        <input
          type="checkbox"
          bind:checked={adminMode}
          on:change={toggleAdminMode}
        />
        <span>관리자 모드</span>
      </label>
      <label class="teams-mode-toggle" class:active={teamsMode}>
        <input
          type="checkbox"
          bind:checked={teamsMode}
          on:change={toggleTeamsMode}
        />
        <span>Teams (Beta)</span>
      </label>
      <label class="plan-mode-toggle" class:active={planMode}>
        <input
          type="checkbox"
          bind:checked={planMode}
          on:change={togglePlanMode}
        />
        <span>Plan 모드</span>
      </label>
      <button class="attach-btn" on:click={handleFileSelect} title="파일 첨부">
        📎 파일 첨부
      </button>
      {#if isGitRepo}
        <button class="review-btn" on:click={requestCodeReview} title="AI 코드 리뷰">
          리뷰
        </button>
        <button class="commit-btn" on:click|stopPropagation title="Git Commit">
          Commit
        </button>
      {/if}
    </div>

    {#if attachedFiles.length > 0}
      <div class="attached-files">
        {#each attachedFiles as file, index}
          <div class="file-item">
            {#if file.preview}
              <img src={file.preview} alt={file.name} class="file-preview" />
            {:else}
              <div class="file-icon-badge">
                <span class="file-ext">{file.name.split('.').pop()}</span>
              </div>
            {/if}
            <div class="file-info">
              <span class="file-name">{file.name}</span>
              <span class="file-path">{shortenPath(file.path)}</span>
            </div>
            {#if !file.preview}
              <input class="line-range-input" placeholder="예: 10-30"
                     bind:value={file.lineRange}
                     on:change={() => attachedFiles = attachedFiles} />
            {/if}
            <button class="remove-file" on:click={() => removeFile(index)} title="제거">✕</button>
          </div>
        {/each}
      </div>
    {/if}

    <div class="input-row">
      <textarea
        bind:this={textareaEl}
        bind:value={inputMessage}
        on:keydown={handleKeydown}
        on:input={handleInput}
        placeholder={currentPlaceholder}
        rows="5"
        spellcheck="false"
        autocorrect="off"
        autocapitalize="off"
        autocomplete="off"
      />
      {#if sending}
        <button class="stop-btn" on:click={handleCancel} title="생성 중지 (Esc)">
          ■ 중지
        </button>
      {:else}
        <button on:click={handleSend} disabled={!inputMessage.trim() && attachedFiles.length === 0} title="전송 ({isMac ? '⌘' : 'Ctrl'}+Enter)">
          전송 <span class="shortcut-hint">{isMac ? '⌘' : 'Ctrl'}↵</span>
        </button>
      {/if}
    </div>
  </div>
</div>

{#if lightboxSrc}
  <div class="lightbox-overlay" on:click={closeLightbox}>
    <img src={lightboxSrc} alt="enlarged" class="lightbox-img" on:click|stopPropagation />
    <button class="lightbox-close" on:click={closeLightbox}>✕</button>
  </div>
{/if}

<style>
  .conversation-tab {
    display: flex;
    flex-direction: column;
    height: 100%;
    position: relative;
    --wails-drop-target: drop;
  }

  .conversation-tab.drag-over {
    outline: 2px dashed var(--accent);
    outline-offset: -4px;
  }

  .conversation-tab.drag-over .messages-area {
    opacity: 0.7;
  }

  .messages-area {
    flex: 1;
    overflow-y: auto;
    padding: 20px;
    background-color: var(--bg-tertiary);
    display: flex;
    flex-direction: column;
    align-items: stretch;
  }

  /* Custom scrollbar */
  .messages-area::-webkit-scrollbar {
    width: 8px;
  }

  .messages-area::-webkit-scrollbar-track {
    background: var(--scrollbar-track);
  }

  .messages-area::-webkit-scrollbar-thumb {
    background: var(--scrollbar-thumb);
    border-radius: 4px;
  }

  .messages-area::-webkit-scrollbar-thumb:hover {
    background: var(--scrollbar-thumb-hover);
  }

  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    height: 100%;
    color: var(--text-muted);
    gap: 1em;
  }

  .empty-state p {
    font-size: 1.1em;
    margin: 0;
  }

  .empty-state::before {
    content: '💬';
    font-size: 4em;
    opacity: 0.3;
  }

  .message {
    margin-bottom: 12px;
    padding: 8px 12px;
    border-radius: 8px;
    animation: fadeIn 0.3s ease-in;
    max-width: 65%;
    width: fit-content;
    display: block;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(10px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .message.user {
    background-color: var(--bg-message-user);
    color: var(--text-primary);
    margin-left: auto;
    margin-right: 0;
    padding: 6px 12px;
  }

  .message.assistant {
    background-color: var(--bg-message-assistant);
    margin-left: 0;
    margin-right: auto;
  }

  .message.system {
    background-color: var(--bg-message-system);
    border-left: 3px solid var(--accent);
    margin-left: 0;
    margin-right: auto;
    max-width: 80%;
  }

  .message.streaming {
    opacity: 0.95;
    max-width: 80%;
  }

  .message.loading {
    opacity: 0.7;
  }

  .loading-dots::after {
    content: '...';
    animation: dots 1.5s steps(4, end) infinite;
  }

  @keyframes dots {
    0%, 20% {
      content: '.';
    }
    40% {
      content: '..';
    }
    60%, 100% {
      content: '...';
    }
  }

  .streaming-indicator {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px solid var(--border-primary);
  }

  .streaming-dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background-color: var(--accent);
    animation: streamPulse 1.2s ease-in-out infinite;
  }

  .streaming-dot:nth-child(2) {
    animation-delay: 0.2s;
  }

  .streaming-dot:nth-child(3) {
    animation-delay: 0.4s;
  }

  @keyframes streamPulse {
    0%, 80%, 100% {
      opacity: 0.3;
      transform: scale(0.8);
    }
    40% {
      opacity: 1;
      transform: scale(1);
    }
  }

  .streaming-label {
    font-size: 11px;
    color: var(--text-muted);
    margin-left: 4px;
    max-width: 300px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .streaming-tool-icon {
    font-size: 12px;
    margin-left: 4px;
  }

  .streaming-tokens {
    display: flex;
    gap: 6px;
    margin-left: 8px;
    font-size: 10px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .token-in {
    color: #60a5fa;
  }

  .token-out {
    color: #34d399;
  }

  .message-attachments {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 8px;
  }

  .code-attachment {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    background-color: var(--bg-tertiary, rgba(0,0,0,0.15));
    border: 1px solid var(--border-primary);
    border-radius: 6px;
  }

  .code-att-ext {
    font-size: 10px;
    font-weight: 600;
    color: var(--accent);
    text-transform: uppercase;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    padding: 2px 6px;
    background-color: var(--accent-bg, rgba(0, 120, 212, 0.1));
    border-radius: 3px;
  }

  .code-att-path {
    font-size: 11px;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .message-images {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-bottom: 8px;
  }

  .message-image-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    max-width: 120px;
  }

  .bubble-image-thumb {
    width: 100px;
    height: 100px;
    object-fit: cover;
    border-radius: 6px;
    border: 1px solid var(--border-primary);
    cursor: pointer;
    transition: transform 0.15s;
  }

  .bubble-image-thumb:hover {
    transform: scale(1.05);
  }

  .image-placeholder {
    width: 100px;
    height: 100px;
    display: flex;
    align-items: center;
    justify-content: center;
    background-color: var(--placeholder-bg);
    border: 1px dashed var(--border-secondary);
    border-radius: 6px;
    color: var(--text-muted);
    font-size: 11px;
    text-align: center;
    overflow: hidden;
    padding: 4px;
    word-break: break-all;
  }

  .image-path-label {
    font-size: 10px;
    color: var(--text-muted);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: center;
  }

  .message-content {
    font-size: 14px;
    line-height: 1.4;
    color: var(--text-primary);
    text-align: left;
    user-select: text;
    cursor: text;
  }

  .message.user .message-content {
  }

  .message.user .message-content :global(p) {
    margin: 0;
  }

  .message.user .message-content :global(ol),
  .message.user .message-content :global(ul) {
    margin: 0;
    padding-left: 0;
    list-style-position: inside;
  }

  .message.user .message-content :global(li) {
    margin: 0;
  }

  .error-banner {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    background-color: var(--error-bg);
    border-top: 2px solid var(--error);
    border-bottom: 2px solid var(--error);
    animation: slideDown 0.3s ease-out;
  }

  @keyframes slideDown {
    from {
      opacity: 0;
      transform: translateY(-10px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .error-icon {
    font-size: 20px;
    flex-shrink: 0;
  }

  .error-text {
    flex: 1;
    color: var(--error-text);
    font-size: 13px;
    line-height: 1.4;
    word-break: break-word;
  }

  .error-close {
    background: none;
    border: none;
    color: var(--error-text);
    cursor: pointer;
    padding: 4px 8px;
    font-size: 16px;
    line-height: 1;
    border-radius: 3px;
    transition: all 0.2s;
    flex-shrink: 0;
  }

  .error-close:hover {
    background-color: var(--error);
    color: var(--text-inverse);
  }

  /* CLI panel (interactive area between messages and input) */
  .cli-panel {
    background-color: var(--bg-panel);
    border-top: 1px solid var(--border-primary);
    border-bottom: 1px solid var(--border-primary);
    padding: 12px 16px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 13px;
    animation: cliSlideUp 0.15s ease-out;
    outline: none;
  }

  @keyframes cliSlideUp {
    from { opacity: 0; transform: translateY(8px); }
    to { opacity: 1; transform: translateY(0); }
  }

  .cli-title {
    color: var(--text-muted);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 8px;
  }

  .cli-options {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .cli-option {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    transition: background-color 0.1s;
  }

  .cli-option:hover {
    background-color: var(--bg-hover);
  }

  .cli-option.selected {
    color: var(--accent);
    background-color: var(--accent-bg);
  }

  .cli-cursor {
    width: 12px;
    flex-shrink: 0;
    color: var(--accent);
  }

  .cli-choice-num {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background-color: var(--accent);
    color: var(--text-inverse);
    font-size: 10px;
    font-weight: 700;
    flex-shrink: 0;
  }

  .cli-model-name {
    flex: 1;
  }

  .cli-choice-text {
    flex: 1;
    min-width: 0;
  }

  /* Flatten MarkdownViewer output inside choice items */
  .cli-choice-text :global(p) {
    margin: 0;
    display: inline;
  }

  .cli-choice-text :global(strong) {
    color: var(--text-primary);
  }

  .cli-badge {
    font-size: 10px;
    color: var(--success);
    padding: 1px 6px;
    border: 1px solid rgba(46, 125, 50, 0.4);
    border-radius: 3px;
  }

  .cli-hint {
    margin-top: 10px;
    color: var(--text-muted);
    font-size: 11px;
  }

  .cli-result-content {
    color: var(--text-primary);
    font-size: 13px;
    line-height: 1.5;
  }

  .cli-plan-ready {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px;
  }

  .plan-ready-text {
    color: var(--text-primary);
    font-size: 13px;
    font-weight: 500;
  }

  .plan-execute-btn {
    padding: 6px 16px;
    background-color: #7c3aed;
    color: #ffffff;
    border: none;
    border-radius: 4px;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    transition: background-color 0.2s;
  }

  .plan-execute-btn:hover {
    background-color: #6d28d9;
  }

  .plan-ready-hint {
    color: var(--text-muted);
    font-size: 11px;
    margin-left: auto;
  }

  .plan-view-btn {
    padding: 6px 12px;
    background-color: transparent;
    color: #a78bfa;
    border: 1px solid #7c3aed;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
    transition: background-color 0.2s;
  }

  .plan-view-btn:hover {
    background-color: rgba(124, 58, 237, 0.15);
  }

  .cli-plan-viewer {
    display: flex;
    flex-direction: column;
    max-height: 400px;
    background-color: var(--bg-secondary, #1e1e2e);
    border: 1px solid #7c3aed44;
    border-radius: 8px;
    padding: 12px;
    box-shadow: 0 2px 12px rgba(124, 58, 237, 0.08);
  }

  .plan-viewer-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 4px 10px;
    border-bottom: 1px solid #7c3aed33;
    margin-bottom: 10px;
  }

  .plan-viewer-title {
    color: #a78bfa;
    font-size: 13px;
    font-weight: 600;
    flex: 1;
  }

  .plan-viewer-btn {
    background: transparent;
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 2px 8px;
    font-size: 12px;
    cursor: pointer;
    transition: all 0.2s;
  }

  .plan-viewer-btn:hover {
    background-color: rgba(124, 58, 237, 0.15);
    border-color: #7c3aed66;
    color: #a78bfa;
  }

  .plan-viewer-content {
    overflow-y: auto;
    flex: 1;
    font-size: 13px;
    line-height: 1.5;
    padding: 4px;
  }

  /* AskUserQuestion panel styles */
  .ask-panel {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .ask-progress {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .ask-step {
    display: inline-flex;
    align-items: center;
    padding: 2px 10px;
    border-radius: 12px;
    font-size: 11px;
    font-weight: 500;
    background-color: var(--bg-hover);
    color: var(--text-muted);
    transition: all 0.15s;
  }

  .ask-step.current {
    background-color: var(--accent-bg);
    color: var(--accent);
    font-weight: 600;
  }

  .ask-step.done {
    background-color: rgba(46, 125, 50, 0.15);
    color: var(--success);
  }

  .ask-submit-tab {
    margin-left: auto;
    padding: 3px 12px;
    font-size: 11px;
    font-weight: 600;
    border: 1px solid var(--accent);
    border-radius: 4px;
    background: transparent;
    color: var(--accent);
    cursor: pointer;
    transition: all 0.15s;
  }

  .ask-submit-tab:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .ask-submit-tab:not(:disabled):hover {
    background-color: var(--accent);
    color: var(--text-inverse);
  }

  .ask-question-text {
    color: var(--text-primary);
    font-size: 13px;
    font-weight: 500;
    margin: 4px 0;
  }

  .ask-opt-content {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .ask-opt-label {
    color: var(--text-primary);
    font-weight: 500;
  }

  .ask-opt-desc {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.3;
  }

  .ask-custom-row {
    display: flex;
    gap: 6px;
    padding: 4px 0 4px 24px;
    align-items: center;
  }

  .ask-custom-input {
    flex: 1;
    padding: 6px 10px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    color: var(--text-primary);
    font-size: 13px;
    outline: none;
  }

  .ask-custom-input:focus {
    border-color: var(--accent);
  }

  .ask-custom-submit {
    padding: 6px 12px;
    background-color: var(--accent);
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }

  .ask-custom-submit:hover {
    filter: brightness(1.1);
  }

  .ask-review {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px 0;
  }

  .ask-review-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 8px;
    border-radius: 4px;
    background-color: var(--bg-hover);
  }

  .ask-review-q {
    color: var(--text-muted);
    font-size: 11px;
  }

  .ask-review-a {
    color: var(--accent);
    font-size: 13px;
    font-weight: 500;
  }

  .ask-actions {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }

  .ask-submit-btn {
    padding: 6px 16px;
    background-color: var(--accent);
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    transition: background-color 0.15s;
  }

  .ask-submit-btn:hover {
    filter: brightness(1.1);
  }

  .ask-cancel-btn {
    padding: 6px 16px;
    background: transparent;
    color: var(--text-muted);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    font-size: 13px;
    cursor: pointer;
    transition: all 0.15s;
  }

  .ask-cancel-btn:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
  }

  .input-splitter {
    height: 4px;
    background-color: var(--border-primary);
    cursor: row-resize;
    flex-shrink: 0;
  }

  .input-splitter:hover,
  .input-splitter.active {
    background-color: var(--accent);
  }

  :global(body.input-resizing) {
    user-select: none;
    cursor: row-resize;
  }

  .input-area {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px;
    background-color: var(--bg-secondary);
    flex-shrink: 0;
    min-height: 100px;
  }


  .input-controls {
    display: flex;
    align-items: center;
    padding: 4px 0;
    gap: 12px;
  }

  .admin-mode-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    cursor: pointer;
    user-select: none;
  }

  .admin-mode-toggle input[type="checkbox"] {
    width: 14px;
    height: 14px;
    cursor: pointer;
  }

  .plan-mode-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    cursor: pointer;
    user-select: none;
    padding: 2px 8px;
    border-radius: 4px;
    transition: all 0.2s;
  }

  .plan-mode-toggle.active {
    color: #7c3aed;
    background-color: rgba(124, 58, 237, 0.1);
  }

  .plan-mode-toggle input[type="checkbox"] {
    width: 14px;
    height: 14px;
    cursor: pointer;
    accent-color: #7c3aed;
  }

  .teams-mode-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-secondary);
    font-size: 12px;
    cursor: pointer;
    user-select: none;
    padding: 2px 8px;
    border-radius: 4px;
    transition: all 0.2s;
  }

  .teams-mode-toggle.active {
    color: #0ea5e9;
    background-color: rgba(14, 165, 233, 0.1);
  }

  .teams-mode-toggle input[type="checkbox"] {
    width: 14px;
    height: 14px;
    cursor: pointer;
    accent-color: #0ea5e9;
  }

  .attach-btn {
    padding: 6px 12px;
    background-color: var(--bg-header);
    color: var(--text-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    cursor: pointer;
    font-size: 12px;
    transition: background-color 0.2s;
  }

  .attach-btn:hover {
    background-color: var(--bg-hover);
  }

  .review-btn {
    padding: 6px 12px;
    background: none;
    border: 1px solid rgba(59, 130, 246, 0.4);
    color: #3b82f6;
    font-size: 12px;
    font-weight: 600;
    border-radius: 4px;
    cursor: pointer;
    white-space: nowrap;
    transition: all 0.15s;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .review-btn:hover {
    background-color: rgba(59, 130, 246, 0.1);
    border-color: #3b82f6;
  }

  .commit-btn {
    padding: 6px 12px;
    background: none;
    border: 1px solid rgba(16, 185, 129, 0.4);
    color: #10b981;
    font-size: 12px;
    font-weight: 600;
    border-radius: 4px;
    cursor: pointer;
    white-space: nowrap;
    transition: all 0.15s;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .commit-btn:hover {
    background-color: rgba(16, 185, 129, 0.1);
    border-color: #10b981;
  }

  .attached-files {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding: 8px 0;
  }

  .file-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background-color: var(--file-item-bg);
    border-radius: 4px;
    border: 1px solid var(--border-primary);
  }

  .file-preview {
    width: 40px;
    height: 40px;
    object-fit: cover;
    border-radius: 4px;
  }

  .file-icon {
    font-size: 24px;
  }

  .file-icon-badge {
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    background-color: var(--accent-bg, rgba(0, 120, 212, 0.1));
    border-radius: 4px;
    border: 1px solid var(--border-primary);
    flex-shrink: 0;
  }

  .file-ext {
    font-size: 10px;
    font-weight: 600;
    color: var(--accent);
    text-transform: uppercase;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }

  .line-range-input {
    width: 80px;
    padding: 4px 8px;
    font-size: 11px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    background-color: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border-primary);
    border-radius: 3px;
    outline: none;
    flex-shrink: 0;
  }

  .line-range-input:focus {
    border-color: var(--accent);
  }

  .line-range-input::placeholder {
    color: var(--text-muted);
    font-style: italic;
  }

  .file-info {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .file-name {
    color: var(--text-primary);
    font-size: 12px;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-path {
    color: var(--text-muted);
    font-size: 10px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .remove-file {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 4px;
    font-size: 14px;
    line-height: 1;
    border-radius: 3px;
    transition: all 0.2s;
  }

  .remove-file:hover {
    background-color: var(--error);
    color: var(--text-inverse);
  }

  .input-row {
    display: flex;
    gap: 12px;
    flex: 1;
    min-height: 0;
  }

  textarea {
    flex: 1;
    padding: 12px;
    background-color: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border-primary);
    border-radius: 4px;
    font-family: inherit;
    font-size: 14px;
    resize: none;
    transition: border-color 0.2s, box-shadow 0.2s;
    min-height: 0;
    height: 100%;
  }

  textarea:focus {
    outline: none;
    border-color: var(--accent);
    box-shadow: 0 0 0 2px rgba(0, 120, 212, 0.2);
  }

  textarea::placeholder {
    color: var(--text-muted);
    font-style: italic;
  }

  button {
    padding: 12px 32px;
    background-color: var(--accent);
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 14px;
    transition: background-color 0.2s;
  }

  .shortcut-hint {
    font-size: 11px;
    opacity: 0.7;
    margin-left: 4px;
  }

  button:hover:not(:disabled) {
    background-color: var(--accent-hover);
  }

  button:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .stop-btn {
    padding: 12px 32px;
    background-color: var(--error);
    color: var(--text-inverse);
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font-size: 14px;
    transition: background-color 0.2s;
    animation: fadeIn 0.2s ease-in;
  }

  .stop-btn:hover {
    background-color: #c50f1f;
  }

  .message.user {
    position: relative;
  }

  .msg-copy-btn,
  .msg-retry-btn {
    position: absolute;
    bottom: -8px;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 1px solid var(--border-primary);
    border-radius: 50%;
    background-color: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, background-color 0.15s, color 0.15s;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .msg-copy-btn {
    left: -8px;
  }

  .msg-retry-btn {
    left: 22px;
  }

  .message.user:hover .msg-copy-btn,
  .message.user:hover .msg-retry-btn {
    opacity: 1;
  }

  .msg-copy-btn:hover,
  .msg-retry-btn:hover {
    background-color: var(--accent);
    color: var(--text-inverse);
    border-color: var(--accent);
  }

  /* --- Clear divider --- */
  .clear-divider {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 16px 0;
    width: 100%;
  }

  .clear-divider-line {
    flex: 1;
    height: 1px;
    background: linear-gradient(to right, transparent, var(--text-muted, #666) 20%, var(--text-muted, #666) 80%, transparent);
    opacity: 0.4;
  }

  .clear-divider-label {
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
    padding: 2px 10px;
    border: 1px solid var(--border-primary);
    border-radius: 10px;
    background-color: var(--bg-secondary);
    opacity: 0.7;
  }

  /* --- Streaming vs Completed color differentiation --- */
  .message.assistant.streaming {
    border-left: 3px solid #f59e0b;
    background-color: var(--bg-message-assistant);
  }

  /* --- Streaming content ellipsis (max-height + fade) --- */
  .streaming-content-wrapper {
    position: relative;
    max-height: 300px;
    overflow-y: scroll;
    scrollbar-width: none;
    -ms-overflow-style: none;
  }
  .streaming-content-wrapper::-webkit-scrollbar {
    display: none;
  }

  .streaming-content-body {
    display: flex;
    flex-direction: column;
  }

  .streaming-content-fade {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 60px;
    background: linear-gradient(to bottom, var(--bg-message-assistant, #2d2d3f), transparent);
    z-index: 1;
    pointer-events: none;
    opacity: 0;
    transition: opacity 0.3s;
  }

  .streaming-content-wrapper.overflowing .streaming-content-fade {
    opacity: 1;
  }

  .message.assistant.completed {
    border-left: 3px solid #10b981;
  }

  .streaming-indicator.waiting {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 0;
  }

  .streaming-elapsed {
    font-size: 11px;
    color: #f59e0b;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    margin-left: auto;
    font-weight: 600;
  }

  /* --- Tool activity history (과정 보기) --- */
  .process-toggle-btn {
    font-size: 11px;
    color: var(--text-secondary);
    cursor: pointer;
    background: none;
    border: none;
    padding: 2px 6px;
    margin-top: 4px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .process-toggle-btn:hover { color: var(--text-primary); }

  .chevron { display: inline-block; transition: transform 0.2s; }
  .chevron.open { transform: rotate(90deg); }

  .process-details {
    margin-top: 6px;
    padding: 8px;
    background: var(--bg-tertiary);
    border-radius: 6px;
    font-size: 12px;
    max-height: 300px;
    overflow-y: auto;
  }

  .process-step {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 0;
    border-bottom: 1px solid var(--border-primary);
  }
  .process-step:last-child { border-bottom: none; }

  .step-num {
    min-width: 20px;
    text-align: center;
    color: var(--text-tertiary);
    font-size: 10px;
  }
  .step-name {
    font-weight: 500;
    white-space: nowrap;
  }
  .step-detail {
    color: var(--text-secondary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* --- Expand button on assistant messages --- */
  .message.assistant {
    position: relative;
  }

  .msg-duration {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--text-muted);
    opacity: 0.6;
    margin-top: 4px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
  }


  .msg-expand-btn {
    position: absolute;
    top: -8px;
    right: -8px;
    width: 26px;
    height: 26px;
    padding: 0;
    border: 1px solid var(--border-primary);
    border-radius: 50%;
    background-color: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, background-color 0.15s, color 0.15s;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .message.assistant:hover .msg-expand-btn {
    opacity: 1;
  }

  .msg-expand-btn:hover {
    background-color: var(--accent);
    color: var(--text-inverse);
    border-color: var(--accent);
  }

  /* Orchestrator Dashboard */
  .orchestrator-dashboard {
    border-top: 1px solid var(--border-primary);
    border-bottom: 1px solid var(--border-primary);
    background-color: var(--bg-header);
    padding: 8px 16px;
    flex-shrink: 0;
  }

  .dashboard-title {
    font-size: 11px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-bottom: 6px;
  }

  .dashboard-tasks {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .task-card {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-radius: 6px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    font-size: 12px;
    transition: all 0.2s;
  }

  .task-card.running {
    border-color: #f59e0b;
    background-color: rgba(245, 158, 11, 0.08);
  }

  .task-card.completed {
    border-color: #10b981;
    background-color: rgba(16, 185, 129, 0.08);
  }

  .task-card.failed {
    border-color: #ef4444;
    background-color: rgba(239, 68, 68, 0.08);
  }

  .task-status-icon {
    width: 16px;
    height: 16px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    font-size: 12px;
    font-weight: 700;
  }

  .task-card.completed .task-status-icon {
    color: #10b981;
  }

  .task-card.failed .task-status-icon {
    color: #ef4444;
  }

  .task-spinner {
    width: 12px;
    height: 12px;
    border: 2px solid #f59e0b;
    border-top-color: transparent;
    border-radius: 50%;
    animation: task-spin 0.8s linear infinite;
  }

  @keyframes task-spin {
    to { transform: rotate(360deg); }
  }

  .task-worker {
    font-weight: 600;
    color: var(--text-primary);
    flex-shrink: 0;
    min-width: 100px;
  }

  .task-desc {
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dashboard-complete {
    margin-top: 6px;
    font-size: 11px;
    font-weight: 600;
    color: #10b981;
    text-align: center;
  }

  /* --- Search bar --- */
  .search-bar {
    position: absolute;
    top: 8px;
    right: 20px;
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 4px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-primary);
    border-radius: 6px;
    padding: 4px 8px;
    box-shadow: 0 2px 8px rgba(0,0,0,0.2);
  }

  .search-bar input {
    width: 200px;
    background: transparent;
    border: none;
    color: var(--text-primary);
    font-size: 13px;
    outline: none;
    padding: 4px;
  }

  .search-bar input::placeholder {
    color: var(--text-muted);
  }

  .search-count {
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: var(--text-muted);
    white-space: nowrap;
    min-width: 40px;
    text-align: center;
  }

  .search-nav-btn,
  .search-close-btn {
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 50%;
    background-color: transparent;
    color: var(--text-muted);
    font-size: 11px;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background-color 0.15s, color 0.15s;
  }

  .search-nav-btn:hover,
  .search-close-btn:hover {
    background-color: var(--bg-hover);
    color: var(--text-primary);
  }

  :global(mark.search-highlight) {
    background-color: rgba(255, 235, 59, 0.4);
    color: inherit;
    padding: 0 1px;
    border-radius: 2px;
  }

  :global(mark.search-highlight.search-current) {
    background-color: rgba(255, 152, 0, 0.6);
    outline: 2px solid rgba(255, 152, 0, 0.8);
  }

  /* --- Scroll to message top button --- */
  .msg-scroll-top-btn {
    position: absolute;
    bottom: -8px;
    right: -8px;
    width: 26px;
    height: 26px;
    padding: 0;
    border: 1px solid var(--border-primary);
    border-radius: 50%;
    background-color: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1;
    cursor: pointer;
    opacity: 0;
    display: none;
    align-items: center;
    justify-content: center;
    transition: opacity 0.15s, background-color 0.15s, color 0.15s;
  }

  .message.long-message .msg-scroll-top-btn {
    display: flex;
  }

  .message.long-message:hover .msg-scroll-top-btn {
    opacity: 1;
  }

  .msg-scroll-top-btn:hover {
    background-color: var(--accent);
    color: var(--text-inverse);
    border-color: var(--accent);
  }

  /* Image lightbox */
  .lightbox-overlay {
    position: fixed;
    top: 0;
    left: 0;
    width: 100vw;
    height: 100vh;
    background: rgba(0, 0, 0, 0.85);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 9999;
    cursor: pointer;
  }

  .lightbox-img {
    max-width: 90vw;
    max-height: 90vh;
    object-fit: contain;
    border-radius: 8px;
    cursor: default;
    box-shadow: 0 4px 40px rgba(0, 0, 0, 0.5);
  }

  .lightbox-close {
    position: absolute;
    top: 16px;
    right: 24px;
    background: rgba(255, 255, 255, 0.15);
    border: none;
    color: #fff;
    font-size: 20px;
    width: 36px;
    height: 36px;
    border-radius: 50%;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.2s;
  }

  .lightbox-close:hover {
    background: rgba(255, 255, 255, 0.3);
  }

</style>
