import { writable } from 'svelte/store';

function createAgentStore() {
  const { subscribe, update } = writable({
    tabRenames: {},           // tabID → suggestedName
    projectSummaries: {},     // workDir → { summary, language, framework }
    codeReviews: {}              // tabID → { issues: [...], summary: "..." }
  });

  return {
    subscribe,

    setTabRename(tabID, name) {
      update(s => {
        s.tabRenames[tabID] = name;
        return { ...s };
      });
    },

    clearTabRename(tabID) {
      update(s => {
        delete s.tabRenames[tabID];
        return { ...s };
      });
    },

    setProjectSummary(workDir, data) {
      update(s => {
        s.projectSummaries[workDir] = data;
        return { ...s };
      });
    },

    clearProjectSummary(workDir) {
      update(s => {
        delete s.projectSummaries[workDir];
        return { ...s };
      });
    },

    setCodeReview(tabID, data) {
      update(s => {
        s.codeReviews[tabID] = data;
        return { ...s };
      });
    },

    clearCodeReview(tabID) {
      update(s => {
        delete s.codeReviews[tabID];
        return { ...s };
      });
    }
  };
}

export const agentStore = createAgentStore();
