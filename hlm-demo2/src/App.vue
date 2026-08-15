<script setup lang="ts">
import { nextTick, ref } from "vue";
import { marked } from "marked";
import DOMPurify from "dompurify";

type Role = "user" | "assistant";

interface Message {
  role: Role;
  content: string;
}

interface StreamEvent {
  event: string;
  data: unknown;
}

interface TypewriterController {
  push: (text: string) => void;
  finish: (fallbackText?: string) => Promise<void>;
  stop: () => void;
}

const loading = ref(false);
const question = ref("");
const messages = ref<Message[]>([
  { role: "assistant", content: "你好，我是红楼梦问答助手。你想了解哪位人物？" },
]);
const TYPEWRITER_BASE_DELAY_MS = 36;

function renderMarkdown(content: string): string {
  const html = marked.parse(content, { breaks: true });
  return DOMPurify.sanitize(typeof html === "string" ? html : "");
}

async function scrollChatToBottom(): Promise<void> {
  await nextTick();
  const box = document.querySelector(".chat-list");
  if (box) {
    box.scrollTop = box.scrollHeight;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function splitIntoTypingUnits(text: string): string[] {
  const units: string[] = [];
  let buffer = "";

  function flush(): void {
    if (buffer) {
      units.push(buffer);
      buffer = "";
    }
  }

  for (const char of Array.from(text)) {
    if (/\s/.test(char)) {
      buffer += char;
      flush();
      continue;
    }

    if (/[a-zA-Z0-9]/.test(char)) {
      buffer += char;
      continue;
    }

    if (/[\u4e00-\u9fff]/.test(char)) {
      buffer += char;
      if (Array.from(buffer).length >= 2) {
        flush();
      }
      continue;
    }

    if (/[,.;!?，。！？；：]/.test(char)) {
      buffer += char;
      flush();
      continue;
    }

    flush();
    units.push(char);
  }

  flush();
  return units;
}

function getTypingDelay(unit: string): number {
  if (!unit.trim()) {
    return 0;
  }

  if (/[。！？!?]/.test(unit)) {
    return 240;
  }

  if (/[，；：,;:]/.test(unit)) {
    return 140;
  }

  if (/^[a-zA-Z0-9]/.test(unit)) {
    return 55;
  }

  const cjkLength = Array.from(unit).filter((char) => /[\u4e00-\u9fff]/.test(char)).length;
  if (cjkLength >= 2) {
    return 70;
  }

  return TYPEWRITER_BASE_DELAY_MS;
}

function createTypewriter(message: Message): TypewriterController {
  const queue: string[] = [];
  let active = true;
  let finished = false;
  let pumping = false;

  let resolveIdle!: () => void;
  const idlePromise = new Promise<void>((resolve) => {
    resolveIdle = resolve;
  });

  async function pump(): Promise<void> {
    if (pumping) {
      return;
    }
    pumping = true;

    while (active) {
      const nextUnit = queue.shift();
      if (!nextUnit) {
        break;
      }
      message.content += nextUnit;
      void scrollChatToBottom();
      await sleep(getTypingDelay(nextUnit));
    }

    pumping = false;
    if (finished && queue.length === 0) {
      resolveIdle();
    }
  }

  return {
    push(text: string) {
      if (!active || !text) {
        return;
      }
      queue.push(...splitIntoTypingUnits(text));
      void pump();
    },
    async finish(fallbackText?: string) {
      if (
        active &&
        queue.length === 0 &&
        !message.content &&
        typeof fallbackText === "string" &&
        fallbackText
      ) {
        queue.push(...splitIntoTypingUnits(fallbackText));
        void pump();
      }

      finished = true;
      void pump();
      await idlePromise;
    },
    stop() {
      active = false;
      queue.length = 0;
      finished = true;
      resolveIdle();
    },
  };
}

function parseStreamEvent(block: string): StreamEvent | null {
  const lines = block.split(/\r?\n/);
  let event = "message";
  const dataLines: string[] = [];

  for (const line of lines) {
    if (line.startsWith("event:")) {
      event = line.slice("event:".length).trim();
      continue;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trim());
    }
  }

  if (dataLines.length === 0) {
    return null;
  }

  const rawData = dataLines.join("\n");
  try {
    return {
      event,
      data: JSON.parse(rawData) as unknown,
    };
  } catch {
    return {
      event,
      data: rawData,
    };
  }
}

async function readStream(
  response: Response,
  onEvent: (payload: StreamEvent) => void,
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) {
    throw new Error("当前环境不支持流式读取");
  }

  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });

    let boundary = buffer.indexOf("\n\n");
    while (boundary !== -1) {
      const block = buffer.slice(0, boundary).trim();
      buffer = buffer.slice(boundary + 2);
      if (block) {
        const parsed = parseStreamEvent(block);
        if (parsed) {
          onEvent(parsed);
        }
      }
      boundary = buffer.indexOf("\n\n");
    }

    if (done) {
      break;
    }
  }

  const lastBlock = buffer.trim();
  if (lastBlock) {
    const parsed = parseStreamEvent(lastBlock);
    if (parsed) {
      onEvent(parsed);
    }
  }
}

async function sendMessage(): Promise<void> {
  const input = question.value.trim();
  if (!input || loading.value) {
    return;
  }

  messages.value.push({ role: "user", content: input });
  messages.value.push({ role: "assistant", content: "" });
  const assistantMessage = messages.value[messages.value.length - 1];
  const typewriter = createTypewriter(assistantMessage);
  question.value = "";
  loading.value = true;
  await scrollChatToBottom();

  try {
    const response = await fetch("/hongloumeng/chat", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
      },
      body: JSON.stringify({ question: input }),
    });

    if (!response.ok) {
      const errorText = await response.text();
      let errorMessage = "请求失败";
      try {
        const data = JSON.parse(errorText) as { error?: string };
        errorMessage = data.error ?? errorMessage;
      } catch {
        if (errorText.trim()) {
          errorMessage = errorText.trim();
        }
      }
      throw new Error(errorMessage);
    }

    let receivedChunk = false;
    await readStream(response, ({ event, data }) => {
      if (event === "chunk") {
        const content =
          typeof data === "object" &&
          data !== null &&
          "content" in data &&
          typeof data.content === "string"
            ? data.content
            : "";

        if (!content) {
          return;
        }

        receivedChunk = true;
        typewriter.push(content);
        return;
      }

      if (event === "done") {
        const answer =
          typeof data === "object" &&
          data !== null &&
          "answer" in data &&
          typeof data.answer === "string"
            ? data.answer
            : "";

        if (!receivedChunk && answer) {
          receivedChunk = true;
          typewriter.push(answer);
        }
        return;
      }

      if (event === "error") {
        const errorMessage =
          typeof data === "object" &&
          data !== null &&
          "error" in data &&
          typeof data.error === "string"
            ? data.error
            : "请求失败";
        throw new Error(errorMessage);
      }
    });

    await typewriter.finish(receivedChunk ? undefined : "未返回答案");
  } catch (error) {
    typewriter.stop();
    const errMsg = error instanceof Error ? error.message : "未知错误";
    assistantMessage.content = `调用接口失败：${errMsg}`;
  } finally {
    loading.value = false;
    await scrollChatToBottom();
  }
}
</script>

<template>
  <el-container class="page">
    <el-header class="header">红楼梦问答</el-header>
    <el-main class="main">
      <div class="chat-list">
        <div
          v-for="(item, index) in messages"
          :key="index"
          class="message-row"
          :class="item.role"
        >
          <el-card shadow="hover" class="bubble">
            <div
              v-if="item.role === 'assistant'"
              class="assistant-content"
            >
              <div
                class="markdown-content"
                v-html="renderMarkdown(item.content)"
              />
            </div>
            <div v-else>{{ item.content }}</div>
          </el-card>
        </div>
      </div>
      <div class="input-area">
        <el-input
          v-model="question"
          type="textarea"
          :rows="3"
          resize="none"
          placeholder="输入问题，例如：贾宝玉的外貌如何？"
          @keydown.enter.exact.prevent="sendMessage"
        />
        <el-button type="primary" :loading="loading" @click="sendMessage">
          发送
        </el-button>
      </div>
    </el-main>
  </el-container>
</template>

<style scoped>
.assistant-content {
  min-height: 1.5em;
  line-height: 1.7;
}

.markdown-content {
  display: inline;
}

.markdown-content :deep(p) {
  display: inline;
  margin: 0;
}
</style>
