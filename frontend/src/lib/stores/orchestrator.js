import { writable } from 'svelte/store';

// Orchestrator state store
// Shape: { [adminTabId]: { tasks: { [taskId]: { taskId, workerTabId, description, status, content } }, jobComplete: bool } }
function createOrchestratorStore() {
  const { subscribe, update, set } = writable({});

  function handleEvent(event) {
    console.log('[OrchestratorStore] Event received:', event.type, event);

    update(state => {
      const adminTabId = event.adminTabId;
      if (!adminTabId) return state;

      const tabState = state[adminTabId] || { tasks: {}, jobComplete: false };

      switch (event.type) {
        case 'task-started':
          // 같은 워커의 기존 running 태스크 완료 처리
          for (const tid in tabState.tasks) {
            if (tabState.tasks[tid].workerTabId === event.workerTabId &&
                tabState.tasks[tid].status === 'running') {
              tabState.tasks[tid] = { ...tabState.tasks[tid], status: 'completed' };
            }
          }
          tabState.tasks = {
            ...tabState.tasks,
            [event.taskId]: {
              taskId: event.taskId,
              workerTabId: event.workerTabId,
              description: event.content || '',
              status: 'running',
              content: ''
            }
          };
          tabState.jobComplete = false;
          break;

        case 'task-completed':
          if (tabState.tasks[event.taskId]) {
            tabState.tasks = {
              ...tabState.tasks,
              [event.taskId]: {
                ...tabState.tasks[event.taskId],
                status: 'completed',
                content: event.content || ''
              }
            };
          }
          break;

        case 'task-failed':
          if (tabState.tasks[event.taskId]) {
            tabState.tasks = {
              ...tabState.tasks,
              [event.taskId]: {
                ...tabState.tasks[event.taskId],
                status: 'failed',
                content: event.content || ''
              }
            };
          }
          break;

        case 'job-completed':
          const failed = event.status === 'failed';
          const updatedTasks = { ...tabState.tasks };
          for (const taskId in updatedTasks) {
            if (updatedTasks[taskId].status === 'running') {
              updatedTasks[taskId] = {
                ...updatedTasks[taskId],
                status: failed ? 'failed' : 'completed'
              };
            }
          }
          tabState.tasks = updatedTasks;
          tabState.jobComplete = true;
          break;
      }

      return { ...state, [adminTabId]: { ...tabState } };
    });
  }

  function clear(adminTabId) {
    update(state => {
      const newState = { ...state };
      delete newState[adminTabId];
      return newState;
    });
  }

  return {
    subscribe,
    handleEvent,
    clear
  };
}

export const orchestratorStore = createOrchestratorStore();
