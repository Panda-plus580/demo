<script setup>
import { ref, nextTick, onMounted } from "vue";

const input = ref("");
const loading = ref(false);
const dbOk = ref(false);
const tools = ref([]);
const messages = ref([]);
const msgBox = ref(null);

const quickPrompts = [
  "查询所有用户",
  "查询 id 为 1 的用户",
  "新增一个用户,名字叫小明,邮箱 xiaoming@test.com,年龄 20",
  "把 id 为 1 的用户年龄改成 30",
  "删除 id 为 2 的用户",
  "北京今天天气怎么样?",
];

/** 美化打印工具返回的 JSON */
function pretty(str) {
  if (typeof str !== "string") return JSON.stringify(str);
  try {
    return JSON.stringify(JSON.parse(str), null, 2);
  } catch {
    return str;
  }
}

function scrollBottom() {
  nextTick(() => {
    if (msgBox.value) msgBox.value.scrollTop = msgBox.value.scrollHeight;
  });
}

/** 点击左侧工具项时,自动填入对应的试问语句 */
function useTool(t) {
  const tips = {
    list_users: "查询所有用户",
    get_user: "查询 id 为 1 的用户",
    create_user: "新增一个用户,名字叫小明,邮箱 xiaoming@test.com,年龄 20",
    update_user: "把 id 为 1 的用户年龄改成 30",
    delete_user: "删除 id 为 2 的用户",
    get_weather: "北京今天天气怎么样?",
  };
  send(tips[t.name] || t.name);
}

async function loadTools() {
  const res = await fetch("/api/tools");
  const json = await res.json();
  tools.value = json.data;
}

async function checkHealth() {
  try {
    const res = await fetch("/api/health");
    const json = await res.json();
    dbOk.value = json.code === 0;
  } catch {
    dbOk.value = false;
  }
}

async function send(preset) {
  const message = (preset ?? input.value).trim();
  if (!message || loading.value) return;
  input.value = "";
  messages.value.push({ role: "user", content: message });
  loading.value = true;
  scrollBottom();
  try {
    const res = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message }),
    });
    const json = await res.json();
    if (json.code === 0) {
      messages.value.push({
        role: "assistant",
        content: json.data.content,
        steps: json.data.steps,
      });
    } else {
      messages.value.push({
        role: "assistant",
        content: "😅 出错了:" + json.message,
      });
    }
  } catch (err) {
    messages.value.push({
      role: "assistant",
      content: "😅 请求失败,请确认后端服务已启动:" + err.message,
    });
  } finally {
    loading.value = false;
    scrollBottom();
  }
}

onMounted(() => {
  loadTools();
  checkHealth();
});
</script>

<template>
  <div class="page">
    <!-- 顶部 -->
    <header class="app-header">
      <span class="title">🤖 LangChain Tool 演示</span>
      <span class="sub">qwen-plus · MySQL 增删改查 · 天气查询</span>
      <div style="flex: 1"></div>
      <el-tag size="small" :type="dbOk ? 'success' : 'danger'">
        {{ dbOk ? "MySQL 已连接" : "MySQL 未连接" }}
      </el-tag>
    </header>

    <div class="app-body">
      <!-- 左侧工具列表 -->
      <aside class="side">
        <h3>📦 可用 Tool(点击试用)</h3>
        <div
          v-for="t in tools"
          :key="t.name"
          class="tool-item"
          @click="useTool(t)"
        >
          <div class="name">{{ t.name }}</div>
          <div class="desc">{{ t.description }}</div>
        </div>
      </aside>

      <!-- 聊天区 -->
      <main class="chat">
        <div class="chat-messages" ref="msgBox">
          <div v-if="messages.length === 0" class="empty-tip">
            <div class="big">👋</div>
            <div style="font-size: 16px; color: #606266; margin-bottom: 6px">
              你好!我是你的智能助手
            </div>
            <div style="font-size: 13px">
              可以让我帮你查询/新增/修改/删除用户,或者查询城市天气
            </div>
          </div>

          <template v-for="(m, i) in messages" :key="i">
            <div class="msg" :class="m.role">
              <div class="avatar">{{ m.role === "user" ? "🧑" : "🤖" }}</div>
              <div style="max-width: 78%">
                <div class="bubble">{{ m.content }}</div>
                <div v-if="m.steps && m.steps.length" class="tool-steps">
                  <el-collapse>
                    <el-collapse-item
                      :title="`工具调用过程(${m.steps.length} 次)`"
                      name="1"
                    >
                      <div v-for="(s, j) in m.steps" :key="j" class="tool-step">
                        <div class="head">
                          <el-icon><Tools /></el-icon>
                          <span class="tname">{{ s.name }}</span>
                          <span class="args">({{ JSON.stringify(s.args) }})</span>
                        </div>
                        <div class="result">{{ pretty(s.result) }}</div>
                      </div>
                    </el-collapse-item>
                  </el-collapse>
                </div>
              </div>
            </div>
          </template>

          <div v-if="loading" class="msg assistant">
            <div class="avatar">🤖</div>
            <div class="bubble">
              <div class="typing"><span></span><span></span><span></span></div>
            </div>
          </div>
        </div>

        <!-- 输入区 -->
        <div class="chat-input">
          <div class="quick">
            <el-tag
              v-for="q in quickPrompts"
              :key="q"
              size="small"
              style="cursor: pointer"
              @click="send(q)"
            >
              {{ q }}
            </el-tag>
          </div>
          <div class="input-row">
            <el-input
              v-model="input"
              placeholder="请输入指令,例如:查询所有用户 / 北京天气怎么样?"
              size="large"
              clearable
              @keyup.enter="send()"
            />
            <el-button type="primary" size="large" :loading="loading" @click="send()">
              发送
            </el-button>
          </div>
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.page {
  height: 100vh;
  display: flex;
  flex-direction: column;
}

.app-header {
  display: flex;
  align-items: center;
  gap: 12px;
  background: linear-gradient(90deg, #1f2d3d, #409eff);
  color: #fff;
  padding: 0 20px;
  height: 56px;
  flex-shrink: 0;
}

.app-header .title {
  font-size: 17px;
  font-weight: 600;
  letter-spacing: 1px;
}

.app-header .sub {
  font-size: 12px;
  opacity: 0.75;
}

.app-body {
  flex: 1;
  display: flex;
  overflow: hidden;
}

.side {
  width: 300px;
  background: #fff;
  border-right: 1px solid #e4e7ed;
  display: flex;
  flex-direction: column;
  padding: 16px;
  overflow-y: auto;
  flex-shrink: 0;
}

.side h3 {
  font-size: 14px;
  color: #303133;
  margin-bottom: 12px;
}

.tool-item {
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 10px;
  cursor: pointer;
  transition: all 0.2s;
}

.tool-item:hover {
  border-color: #409eff;
  background: #f5f9ff;
}

.tool-item .name {
  font-weight: 600;
  font-size: 13px;
  color: #409eff;
  font-family: Menlo, Consolas, monospace;
}

.tool-item .desc {
  font-size: 12px;
  color: #606266;
  margin-top: 4px;
  line-height: 1.5;
}

.chat {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.chat-messages {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
}

.msg {
  display: flex;
  margin-bottom: 18px;
}

.msg.user {
  justify-content: flex-end;
}

.msg .avatar {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 15px;
  flex-shrink: 0;
  margin-top: 2px;
}

.msg.user .avatar {
  background: #409eff;
  order: 2;
  margin-left: 10px;
}

.msg.assistant .avatar {
  background: #67c23a;
  margin-right: 10px;
}

.bubble {
  max-width: 70%;
  padding: 12px 16px;
  border-radius: 12px;
  font-size: 14px;
  line-height: 1.7;
  word-break: break-word;
  white-space: pre-wrap;
}

.msg.user .bubble {
  background: #409eff;
  color: #fff;
  border-top-right-radius: 2px;
}

.msg.assistant .bubble {
  background: #fff;
  color: #303133;
  border: 1px solid #e4e7ed;
  border-top-left-radius: 2px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
}

.tool-steps {
  margin-top: 8px;
  width: 100%;
}

.tool-step {
  background: #f8f9fb;
  border: 1px dashed #dcdfe6;
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 8px;
  font-size: 12px;
}

.tool-step .head {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #606266;
  font-weight: 600;
}

.tool-step .head .tname {
  color: #409eff;
  font-family: Menlo, Consolas, monospace;
}

.tool-step .head .args {
  color: #909399;
  font-family: Menlo, Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-step .result {
  margin-top: 6px;
  background: #fff;
  border-radius: 6px;
  padding: 8px 10px;
  color: #606266;
  font-family: Menlo, Consolas, monospace;
  white-space: pre-wrap;
  max-height: 160px;
  overflow-y: auto;
}

.typing {
  display: inline-flex;
  gap: 4px;
  align-items: center;
}

.typing span {
  width: 6px;
  height: 6px;
  background: #909399;
  border-radius: 50%;
  animation: blink 1.2s infinite;
}

.typing span:nth-child(2) {
  animation-delay: 0.2s;
}

.typing span:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes blink {
  0%,
  80%,
  100% {
    opacity: 0.2;
    transform: scale(0.8);
  }
  40% {
    opacity: 1;
    transform: scale(1);
  }
}

.chat-input {
  background: #fff;
  border-top: 1px solid #e4e7ed;
  padding: 12px 20px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex-shrink: 0;
}

.quick {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.input-row {
  display: flex;
  gap: 10px;
}

.empty-tip {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: #909399;
}

.empty-tip .big {
  font-size: 42px;
  margin-bottom: 12px;
}
</style>
