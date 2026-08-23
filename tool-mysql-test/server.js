import express from "express";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { ChatOpenAI } from "@langchain/openai";
import dotenv from "dotenv";
import { userTools } from "./tools/users.tool.js";
import { weatherTools } from "./tools/weather.tool.js";
import { checkDbConnection } from "./db.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// 加载 .env(基于本文件路径,保证任意目录下启动都能读到)
dotenv.config({ path: path.join(__dirname, ".env") });

const app = express();
app.use(express.json());
// 托管前端构建产物(生产环境先运行 npm run web:build 生成 dist/)
app.use(express.static(path.join(__dirname, "dist")));

const PORT = process.env.PORT || 3000;

/*  1. 汇总所有 Tool  */
const tools = [...userTools, ...weatherTools];
const toolsMap = Object.fromEntries(tools.map((t) => [t.name, t]));

/* 2. 创建 qwen-plus(通过阿里云百炼兼容模式) */
const llm = new ChatOpenAI({
  model: "qwen-plus",
  apiKey: process.env.DASHSCOPE_API_KEY,
  configuration: {
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  },
  temperature: 0,
});

// 把 Tool 绑定给 LLM
const llmWithTools = llm.bindTools(tools);

/* 3. 系统提示词 */
const SYSTEM_PROMPT = `你是一个智能助手,拥有以下能力:
- 通过 MySQL 工具对 users 用户表进行增删改查(list_users / get_user / create_user / update_user / delete_user)
- 通过 get_weather 工具查询城市天气

使用规则:
1. 用户需要查数据、改数据、新增或删除用户时,调用对应的 MySQL 工具,不要凭空编造数据。
2. 用户查询天气时,调用 get_weather 工具。
3. 工具执行完成后,用自然语言把结果清晰地回复给用户。
4. 如果用户没有明确说明 id,但需要修改/删除时,先调用 list_users 或 get_user 找到对应的用户。
5. 回答尽量简洁、口语化。`;

/* 4. 工具调用 Agent 循环 */
async function runAgent(userMessage) {
  const messages = [
    { role: "system", content: SYSTEM_PROMPT },
    { role: "user", content: userMessage },
  ];

  const steps = []; // 记录工具调用过程,方便前端展示
  let response = await llmWithTools.invoke(messages);

  // 只要 LLM 决定调用工具,就循环执行
  while (response.tool_calls?.length > 0) {
    messages.push(response);

    for (const toolCall of response.tool_calls) {
      const name = toolCall.name;
      const args = toolCall.args;
      console.log(`调用工具: ${name}(${JSON.stringify(args)})`);

      let toolResult;
      try {
        toolResult = await toolsMap[name].invoke(args);
      } catch (err) {
        toolResult = JSON.stringify({ error: err.message || String(err) });
      }

      steps.push({ name, args, result: toolResult });

      // 把工具结果作为 tool 角色消息返回给 LLM
      messages.push({
        role: "tool",
        content: toolResult,
        tool_call_id: toolCall.id,
      });
    }

    response = await llmWithTools.invoke(messages);
  }

  return { content: response.content, steps };
}

/* 5. API 接口 */

// 聊天接口:接收用户消息,返回 Agent 最终回答 + 工具调用过程
app.post("/api/chat", async (req, res) => {
  try {
    const { message } = req.body;
    if (!message?.trim()) {
      return res.status(400).json({ code: 1, message: "消息不能为空" });
    }
    console.log(`收到消息: ${message}`);
    const result = await runAgent(message.trim());
    res.json({ code: 0, data: result });
  } catch (err) {
    console.error("❌ 处理失败:", err);
    res.status(500).json({ code: 1, message: err.message || String(err) });
  }
});

// 返回工具列表,供前端展示当前可用的能力
app.get("/api/tools", (_req, res) => {
  res.json({
    code: 0,
    data: tools.map((t) => ({ name: t.name, description: t.description })),
  });
});

// 健康检查(数据库连通性)
app.get("/api/health", async (_req, res) => {
  try {
    await checkDbConnection();
    res.json({ code: 0, data: { db: "ok" } });
  } catch (err) {
    res.status(500).json({ code: 1, message: err.message });
  }
});

/* 6. 启动 */
app.listen(PORT, async () => {
  try {
    await checkDbConnection();
    console.log("✅ MySQL 连接正常");
  } catch (err) {
    console.error(`❌ MySQL 连接失败: ${err.message},请先运行 npm run init-db`);
  }
  console.log(`🚀 服务已启动: http://localhost:${PORT}`);
});
