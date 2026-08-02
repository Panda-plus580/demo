import "dotenv/config";
import http from "http";
import express from "express";
import { WebSocketServer, WebSocket } from "ws";
import { ChatOpenAI } from "@langchain/openai";

const app = express();
const PORT = process.env.PORT || 3000;

// 创建 HTTP 服务器（Express + WebSocket 共用）
const server = http.createServer(app);

// WebSocket 服务器
const wss = new WebSocketServer({ server, path: "/ws/tts" });

// 托管前端页面
app.use(express.static("public"));
// 解析 JSON 请求体（限制 50MB，音频 base64 数据较大）
app.use(express.json({ limit: "50mb" }));

/**
 * SSE 流式聊天接口
 * POST /api/chat
 * Body: { "message": "你好，请介绍一下你自己" }
 *
 * 也支持 GET /api/chat?message=xxx
 */
app.post("/api/chat", handleChatStream);
app.get("/api/chat", handleChatStream);

async function handleChatStream(req, res) {
  // 从 query 或 body 中获取消息
  const message = req.query.message || (req.body && req.body.message) || "你好";

  // 设置 SSE 响应头
  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.setHeader("X-Accel-Buffering", "no"); // 禁用 nginx 缓冲
  res.flushHeaders();

  const model = new ChatOpenAI({
    model: process.env.MODEL_NAME || "qwen-plus",
    apiKey: process.env.DASHSCOPE_API_KEY,
    configuration: {
      baseURL: process.env.OPENAI_BASE_URL,
    },
    streaming: true,
    temperature: 0.7,
  });

  try {
    const stream = await model.stream(message);

    for await (const chunk of stream) {
      const content = chunk.content;
      if (content) {
        // 用 Buffer 显式指定 UTF-8 编码，避免浏览器编码猜测错误
        const sseData = `data: ${JSON.stringify({ content })}\n\n`;
        res.write(Buffer.from(sseData, "utf-8"));
      }
    }

    // 发送结束标记
    res.write(Buffer.from("data: [DONE]\n\n", "utf-8"));
    res.end();
  } catch (error) {
    console.error("Chat error:", error.message);
    // 如果响应头还没发送，返回 JSON 错误
    if (!res.headersSent) {
      return res.status(500).json({ error: error.message });
    }
    // 否则通过 SSE 发送错误
    res.write(
      Buffer.from(
        `data: ${JSON.stringify({ error: error.message })}\n\n`,
        "utf-8"
      )
    );
    res.write(Buffer.from("data: [DONE]\n\n", "utf-8"));
    res.end();
  }
}

/**
 * 语音转文字接口 (ASR)
 * POST /api/asr
 * Body: { "audio": "data:audio/webm;base64,AAAA..." }
 * 返回: { "text": "识别的文字内容" }
 */
app.post("/api/asr", async (req, res) => {
  try {
    const { audio } = req.body;
    if (!audio) {
      return res.status(400).json({ error: "缺少 audio 字段" });
    }

    console.log("正在调用语音识别...");

    // 调用阿里云百炼 DashScope 多模态生成 API（语音识别模型）
    const response = await fetch(
      "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${process.env.DASHSCOPE_API_KEY}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          model: "qwen3-asr-flash",
          input: {
            messages: [
              {
                role: "user",
                content: [{ audio }],
              },
            ],
          },
          parameters: {
            enable_itn: true, // 中文数字转阿拉伯数字（如"一百"→100）
          },
        }),
      }
    );

    const data = await response.json();

    if (!response.ok) {
      console.error("ASR API 错误:", data);
      return res
        .status(response.status)
        .json({ error: data.message || "语音识别失败" });
    }

    // 提取识别结果: output.choices[0].message.content[0].text
    const text = data?.output?.choices?.[0]?.message?.content?.[0]?.text;

    if (text) {
      console.log("识别结果:", text);
      res.json({ text });
    } else {
      console.error("未获取到识别结果:", JSON.stringify(data));
      res.status(500).json({ error: "未获取到识别结果" });
    }
  } catch (error) {
    console.error("ASR 接口错误:", error.message);
    res.status(500).json({ error: error.message });
  }
});

/**
 * 流式语音合成 (TTS) WebSocket 接口
 * 路径: ws://localhost:3000/ws/tts
 *
 * 浏览器 → 服务器:
 *   { type: "text", data: "要合成的内容" }  追加文本
 *   { type: "finish" }                      文本结束，等待最后音频
 *
 * 服务器 → 浏览器:
 *   { type: "audio", data: "base64 mp3..." }  音频块
 *   { type: "done" }                          合成完成
 *   { type: "error", message: "..." }         错误
 */
wss.on("connection", (browserWs) => {
  console.log("🔊 TTS WebSocket 客户端已连接");

  let dashScopeWs = null;
  let isFinished = false;
  let sessionReady = false;
  let pendingMessages = []; // 缓存 session 就绪前的消息

  // 连接到阿里云百炼实时 TTS
  const ttsUrl = `wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=qwen3-tts-flash-realtime`;

  // 实时语音合成参考：https://help.aliyun.com/zh/model-studio/interactive-process-of-qwen-tts-realtime-synthesis?spm=a2c4g.11186623.help-menu-2400256.d_2_5_1_1_0.158c29ee8q62gF&scm=20140722.H_2963385._.OR_help-T_cn~zh-V_1
  dashScopeWs = new WebSocket(ttsUrl, {
    headers: {
      Authorization: `Bearer ${process.env.DASHSCOPE_API_KEY}`,
    },
  });

  dashScopeWs.on("open", () => {
    console.log("已连接 DashScope TTS");

    // 配置 session：server_commit 模式让服务端自动判断断句与合成时机
    // 通过发送 session.update 事件设置音色、格式、模式等参数。
    dashScopeWs.send(
      JSON.stringify({
        type: "session.update",
        session: {
          mode: "server_commit",
          voice: "Cherry",
          response_format: "mp3",
        },
      })
    );
  });

  dashScopeWs.on("message", (data) => {
    const msg = JSON.parse(data.toString());
    console.log(`DashScope: ${msg.type}`);

    switch (msg.type) {
      case "session.created":
        console.log(`TTS 会话已创建: ${msg.session?.id}`);
        // Session 就绪，发送缓存的消息
        sessionReady = true;
        if (pendingMessages.length > 0) {
          console.log(`发送 ${pendingMessages.length} 条缓存消息`);
          for (const pending of pendingMessages) {
            dashScopeWs.send(JSON.stringify(pending));
          }
          pendingMessages = [];
        }
        break;

      case "response.audio.delta":
        // 将音频块转发给浏览器
        if (browserWs.readyState === WebSocket.OPEN) {
          browserWs.send(JSON.stringify({ type: "audio", data: msg.delta }));
        }
        break;

      case "response.done":
        // 单段音频完成
        break;

      case "session.finished":
        console.log("TTS 合成全部完成");
        if (browserWs.readyState === WebSocket.OPEN) {
          browserWs.send(JSON.stringify({ type: "done" }));
          browserWs.close();
        }
        break;

      default:
        if (msg.type?.includes("error")) {
          console.error("TTS 错误:", msg);
          if (browserWs.readyState === WebSocket.OPEN) {
            browserWs.send(
              JSON.stringify({
                type: "error",
                message: msg.message || "TTS 合成错误",
              })
            );
          }
        }
        break;
    }
  });

  dashScopeWs.on("error", (err) => {
    console.error("DashScope WebSocket 错误:", err.message);
    if (browserWs.readyState === WebSocket.OPEN) {
      browserWs.send(JSON.stringify({ type: "error", message: err.message }));
    }
  });

  dashScopeWs.on("close", () => {
    console.log("DashScope TTS 连接已关闭");
  });

  // 接收浏览器发来的消息
  browserWs.on("message", (data) => {
    try {
      const msg = JSON.parse(data.toString());
      console.log(`收到浏览器消息: type=${msg.type}`);

      if (msg.type === "text" && msg.data) {
        // 缓存消息，等 DashScope session 就绪后再发送
        const ttsMsg = {
          type: "input_text_buffer.append",
          text: msg.data,
        };
        if (sessionReady && dashScopeWs?.readyState === WebSocket.OPEN) {
          console.log(
            `DashScope: input_text_buffer.append (${msg.data.length} chars)`
          );
          dashScopeWs.send(JSON.stringify(ttsMsg));
        } else {
          pendingMessages.push(ttsMsg);
          console.log(
            `缓存消息 (sessionReady=${sessionReady}, wsState=${dashScopeWs?.readyState})`
          );
        }
      } else if (msg.type === "finish") {
        isFinished = true;
        const finishMsg = { type: "session.finish" };
        if (sessionReady && dashScopeWs?.readyState === WebSocket.OPEN) {
          console.log(`DashScope: session.finish`);
          dashScopeWs.send(JSON.stringify(finishMsg));
        } else {
          pendingMessages.push(finishMsg);
          console.log(
            `缓存 finish (sessionReady=${sessionReady}, wsState=${dashScopeWs?.readyState})`
          );
        }
      }
    } catch (err) {
      console.error("解析浏览器消息失败:", err.message);
    }
  });

  browserWs.on("close", () => {
    console.log("TTS 客户端断开");
    // 清理 DashScope 连接
    if (dashScopeWs?.readyState === WebSocket.OPEN) {
      if (!isFinished) {
        dashScopeWs.send(JSON.stringify({ type: "session.finish" }));
      }
      dashScopeWs.close();
    }
  });

  browserWs.on("error", (err) => {
    console.error("浏览器 WebSocket 错误:", err.message);
  });
});

// 健康检查
app.get("/health", (_req, res) => {
  res.json({ status: "ok", model: process.env.MODEL_NAME || "qwen-plus" });
});

server.listen(PORT, () => {
  console.log(`ASR-TTS Service running at http://localhost:${PORT}`);
  console.log(`SSE chat endpoint: http://localhost:${PORT}/api/chat`);
  console.log(`ASR endpoint: http://localhost:${PORT}/api/asr`);
  console.log(`TTS WebSocket: ws://localhost:${PORT}/ws/tts`);
  console.log(`Model: ${process.env.MODEL_NAME || "qwen-plus"}`);
});
