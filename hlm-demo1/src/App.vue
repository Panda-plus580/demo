<script setup lang="ts">
import { nextTick, ref } from "vue";
import { marked } from "marked";
import DOMPurify from "dompurify";

type Role = "user" | "assistant";

interface Message {
  role: Role;
  content: string;
}

const loading = ref(false);
const question = ref("");
const messages = ref<Message[]>([
  { role: "assistant", content: "你好，我是红楼梦问答助手。你想了解哪位人物？" },
]);

function renderMarkdown(content: string): string {
  const html = marked.parse(content, { breaks: true });
  return DOMPurify.sanitize(typeof html === "string" ? html : "");
}

async function sendMessage(): Promise<void> {
  const input = question.value.trim();
  if (!input || loading.value) {
    return;
  }

  messages.value.push({ role: "user", content: input });
  question.value = "";
  loading.value = true;

  try {
    const response = await fetch("/hongloumeng/chat", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ question: input }),
    });
    const data = (await response.json()) as { answer?: string; error?: string };

    if (!response.ok) {
      throw new Error(data.error ?? "请求失败");
    }

    messages.value.push({
      role: "assistant",
      content: data.answer ?? "未返回答案",
    });
  } catch (error) {
    const errMsg = error instanceof Error ? error.message : "未知错误";
    messages.value.push({
      role: "assistant",
      content: `调用接口失败：${errMsg}`,
    });
  } finally {
    loading.value = false;
    await nextTick();
    const box = document.querySelector(".chat-list");
    if (box) {
      box.scrollTop = box.scrollHeight;
    }
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
              class="markdown-content"
              v-html="renderMarkdown(item.content)"
            />
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
