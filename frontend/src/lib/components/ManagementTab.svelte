<script>
  import { onMount } from 'svelte';
  import { GetSettings, UpdateSettings } from '../../../wailsjs/go/main/App';

  let planModeDefault = false;
  let tabSettings = {};
  let loading = false;

  onMount(async () => {
    await loadSettings();
  });

  async function loadSettings() {
    try {
      const settings = await GetSettings();
      planModeDefault = settings.planModeDefault;
      tabSettings = settings.tabSettings || {};
    } catch (error) {
      console.error('Failed to load settings:', error);
    }
  }

  async function saveSettings() {
    loading = true;
    try {
      await UpdateSettings({ planModeDefault, tabSettings });
      alert('설정이 저장되었습니다.');
    } catch (error) {
      console.error('Failed to save settings:', error);
      alert('설정 저장에 실패했습니다.');
    } finally {
      loading = false;
    }
  }
</script>

<div class="management-tab">
  <h1>설정</h1>

  <section class="settings-section">
    <h2>일반 설정</h2>
    <label class="checkbox-label">
      <input type="checkbox" bind:checked={planModeDefault} />
      <span>Plan 모드 기본 사용</span>
    </label>
  </section>

  <section class="settings-section">
    <h2>탭별 옵션</h2>
    {#each [1, 2, 3, 4] as tabNum}
      <label class="checkbox-label">
        <input type="checkbox" bind:checked={tabSettings[`conversation-${tabNum}`]} />
        <span>대화 {tabNum} 옵션</span>
      </label>
    {/each}
  </section>

  <button class="save-button" on:click={saveSettings} disabled={loading}>
    {loading ? '저장 중...' : '설정 저장'}
  </button>
</div>

<style>
  .management-tab {
    padding: 24px;
    max-width: 800px;
    margin: 0 auto;
  }

  h1 {
    font-size: 28px;
    margin-bottom: 24px;
    color: #ffffff;
  }

  h2 {
    font-size: 18px;
    margin-bottom: 16px;
    color: #e0e0e0;
  }

  .settings-section {
    background-color: #2d2d2d;
    padding: 20px;
    border-radius: 8px;
    margin-bottom: 20px;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    padding: 10px 0;
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    margin-right: 12px;
    width: 18px;
    height: 18px;
    cursor: pointer;
  }

  .checkbox-label span {
    font-size: 14px;
    color: #e0e0e0;
  }

  .save-button {
    background-color: #0078d4;
    color: #ffffff;
    border: none;
    padding: 12px 32px;
    font-size: 14px;
    border-radius: 4px;
    cursor: pointer;
    transition: background-color 0.2s;
  }

  .save-button:hover:not(:disabled) {
    background-color: #106ebe;
  }

  .save-button:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
