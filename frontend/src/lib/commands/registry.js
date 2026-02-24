import { ClearConversation, GetUsageInfo, SetModel, ReadFileSnippet } from '../../../wailsjs/go/main/App';

const commands = [
  {
    name: 'help',
    description: '사용 가능한 명령어 목록 표시',
    icon: '❓',
    needsBackend: false,
    handler: async () => {
      const lines = commands.map(cmd => `- \`/${cmd.name}\` — ${cmd.description}`);
      return {
        type: 'message',
        content: `**사용 가능한 명령어:**\n\n${lines.join('\n')}`
      };
    }
  },
  {
    name: 'usage',
    description: '토큰 사용량/비용 표시',
    icon: '📊',
    needsBackend: true,
    handler: async (tabId) => {
      try {
        const info = await GetUsageInfo(tabId);
        return {
          type: 'message',
          content: `**토큰 사용량:**\n\n` +
            `- 입력 토큰: ${info.inputTokens.toLocaleString()}\n` +
            `- 출력 토큰: ${info.outputTokens.toLocaleString()}\n` +
            `- 총 토큰: ${info.totalTokens.toLocaleString()}\n` +
            `- 메시지 수: ${info.messageCount}`
        };
      } catch (error) {
        return { type: 'message', content: `**오류:** ${error}` };
      }
    }
  },
  {
    name: 'clear',
    description: '대화 기록 초기화',
    icon: '🗑️',
    needsBackend: true,
    handler: async (tabId) => {
      try {
        await ClearConversation(tabId);
        return { type: 'action', content: 'clear' };
      } catch (error) {
        return { type: 'message', content: `**오류:** ${error}` };
      }
    }
  },
  {
    name: 'model',
    description: '현재 모델 표시/변경',
    icon: '🤖',
    needsBackend: true,
    handler: async (tabId, args) => {
      try {
        if (args && args.trim()) {
          await SetModel(args.trim());
          return { type: 'message', content: `모델 변경 완료: **${args.trim()}**` };
        }
        // No args case is handled by ConversationTab (interactive selector)
        return { type: 'message', content: '' };
      } catch (error) {
        return { type: 'message', content: `**오류:** ${error}` };
      }
    }
  },
  {
    name: 'snippet',
    description: '코드 파일 조각 첨부 (예: /snippet path/to/file.go 10-30)',
    icon: '📄',
    needsBackend: true,
    handler: async (tabId, args) => {
      if (!args || !args.trim()) {
        return { type: 'message', content: '**사용법:** `/snippet <파일경로> [시작-끝]`\n\n예: `/snippet app.go 10-30`' };
      }
      const parts = args.trim().split(/\s+/);
      const path = parts[0];
      const lineRange = parts[1] || null;

      // Validate snippet exists by trying to read it
      if (lineRange) {
        try {
          const rangeParts = lineRange.split('-').map(Number);
          const start = rangeParts[0] || 1;
          const end = rangeParts[1] || start;
          await ReadFileSnippet(path, start, end);
        } catch (error) {
          return { type: 'message', content: `**오류:** 파일을 읽을 수 없습니다: ${error}` };
        }
      }

      return {
        type: 'attachment',
        data: {
          path: path,
          name: path.split('/').pop(),
          lineRange: lineRange
        }
      };
    }
  },
  {
    name: 'apply',
    description: '플랜 모드의 분석 결과를 실행',
    icon: '▶️',
    needsBackend: false,
    handler: async () => {
      return { type: 'plan-execute' };
    }
  }
];

export function matchCommands(prefix) {
  const lower = prefix.toLowerCase();
  return commands.filter(cmd => cmd.name.startsWith(lower));
}

export function findCommand(name) {
  return commands.find(cmd => cmd.name === name.toLowerCase());
}

export function getAllCommands() {
  return commands;
}
