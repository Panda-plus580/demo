import fs from "fs";
import path from "path";
import dotenv from "dotenv";

dotenv.config();

const API_KEY = process.env.DASHSCOPE_API_KEY;
const AUDIO_FILE = path.resolve("./realtime_output.mp3");

async function transcribeLocalAudio() {
  // 检查本地文件是否存在
  if (!fs.existsSync(AUDIO_FILE)) {
    console.error(`找不到本地音频文件: ${AUDIO_FILE}`);
    return;
  }

  try {
    console.log("正在识别本地音频文件...");

    // 1. 读取音频文件并转为 base64
    const audioBuffer = fs.readFileSync(AUDIO_FILE);
    const audioBase64 = audioBuffer.toString("base64");
    // 构造 data URL（MP3 格式）
    const audioDataUrl = `data:audio/mp3;base64,${audioBase64}`;

    // 2. 调用多模态生成 API，以 messages 格式传入音频 data URL
    const response = await fetch(
      "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${API_KEY}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          model: "qwen3-asr-flash",
          input: {
            messages: [
              {
                role: "user",
                content: [
                  {
                    audio: audioDataUrl,
                  },
                ],
              },
            ],
          },
          parameters: {
            enable_itn: true, // 开启 ITN，将中文数字(如"一百")转为阿拉伯数字(100)
          },
        }),
      }
    );

    if (!response.ok) {
      console.error(`HTTP 错误: ${response.status} ${response.statusText}`);
      const errorBody = await response.text();
      console.error("响应内容:", errorBody);
      return;
    }

    const data = await response.json();

    // 3. 提取识别结果
    // 响应结构: output.choices[0].message.content[0].text
    const text = data?.output?.choices?.[0]?.message?.content?.[0]?.text;

    if (text) {
      console.log("\n========== 识别结果 ==========");
      console.log(text);
      console.log("==============================\n");
    } else {
      console.error(
        "未获取到识别结果，完整响应:",
        JSON.stringify(data, null, 2)
      );
    }
  } catch (error) {
    console.error("语音识别失败:", error.message);
  }
}

transcribeLocalAudio();
