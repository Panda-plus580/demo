import WebSocket from "ws";
import fs from "fs";
import dotenv from "dotenv";

dotenv.config();

// 配置区
const config = {
  apiKey: process.env.DASHSCOPE_API_KEY,
  // 实时语音合成模型（如需指令控制，可替换为 qwen3-tts-instruct-flash-realtime）
  model: "qwen3-tts-flash-realtime",
  voice: "Cherry",
  // 待合成文本（模拟流式输入）
  textChunks: [
    "我不去想是否能够成功",
    "既然选择了远方，便只顾风雨兼程",
    "我不去想能否赢得爱情",
    "既然钟情于玫瑰，就勇敢地吐露真诚",
    "我不去想身后会不会袭来寒风冷雨",
    "既然目标是地平线，留给世界的只能是背影",
    "我不去想未来是平坦还是泥泞",
    "只要热爱生命，一切，都在意料之中",
  ],
  // 新加坡地域需替换 WorkspaceId，北京地域使用 wss://dashscope.aliyuncs.com/api-ws/v1/realtime
  wsUrl: "wss://dashscope.aliyuncs.com/api-ws/v1/realtime",
};

// 1. 建立 WebSocket 连接
const ws = new WebSocket(`${config.wsUrl}?model=${config.model}`, {
  headers: {
    Authorization: `Bearer ${config.apiKey}`,
  },
});

// 2. 处理连接打开事件
ws.on("open", () => {
  console.log("WebSocket 连接已建立");

  // 发送 session.update 配置会话参数
  const sessionUpdate = {
    type: "session.update",
    session: {
      mode: "server_commit", // 服务端智能判断分段与合成时机
      voice: config.voice,
      response_format: "mp3", // 支持 mp3, pcm, wav 等
    },
  };
  ws.send(JSON.stringify(sessionUpdate));

  // 模拟流式发送文本
  sendTextStream();
});

// 3. 模拟流式发送文本
function sendTextStream() {
  let index = 0;
  const interval = setInterval(() => {
    if (index < config.textChunks.length) {
      const text = config.textChunks[index];
      console.log(`发送文本: ${text}`);

      // 追加文本到缓冲区
      ws.send(
        JSON.stringify({
          type: "input_text_buffer.append",
          text: text,
        })
      );
      index++;
    } else {
      clearInterval(interval);
      // 文本发送完毕，通知服务端结束
      ws.send(JSON.stringify({ type: "session.finish" }));
      console.log("文本发送完毕，等待最后音频生成...");
    }
  }, 200); // 每 200ms 发送一段，模拟打字机效果
}

// 4. 处理服务端返回事件
ws.on("message", (data) => {
  const response = JSON.parse(data.toString());

  switch (response.type) {
    case "session.created":
      console.log(`会话已创建，Session ID: ${response.session.id}`);
      break;

    case "response.audio.delta":
      // 接收音频流数据（Base64 编码）
      const audioBuffer = Buffer.from(response.delta, "base64");
      // 实际项目中，这里可以推送到前端播放器或写入文件
      fs.appendFileSync("realtime_output.mp3", audioBuffer);
      process.stdout.write("."); // 简单的进度提示
      break;

    case "response.done":
      console.log("\n单段音频响应完成");
      break;

    case "session.finished":
      console.log("实时语音合成全部完成！");
      ws.close();
      break;

    default:
      // 处理其他事件或错误
      if (response.type?.includes("error")) {
        console.error("服务端错误:", response);
      }
      break;
  }
});

// 5. 错误与关闭处理
ws.on("error", (err) => console.error("WebSocket 错误:", err.message));
ws.on("close", (code, reason) =>
  console.log(`连接已关闭 (Code: ${code}, Reason: ${reason || "Normal"})`)
);
