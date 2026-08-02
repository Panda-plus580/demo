import fs from "fs";
import dotenv from "dotenv";

dotenv.config(); // 从 .env 加载环境变量

const API_KEY = process.env.DASHSCOPE_API_KEY;
const OUTPUT_FILE = "output.mp3"; // 本地保存路径

// 配置区
const config = {
  model: "qwen3-tts-flash",
  voice: "Serena",
  text: "那我来给大家推荐一款T恤, 这款呢真的是超级好看, 这个颜色呢很显气质, 而且呢也是搭配的绝佳单品, 大家可以闭眼入, 真的是非常好看, 对身材的包容性也很好, 不管啥身材的宝宝呢, 穿上去都是很好看的。推荐宝宝们下单哦。",
};

// 核心逻辑
async function generateTTS() {
  try {
    // 1. 调用多模态生成 API（非实时）
    const response = await fetch(
      "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${API_KEY}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          model: config.model,
          input: {
            text: config.text,
            voice: config.voice,
          },
        }),
      }
    );

    const data = await response.json();

    // 2. 提取音频文件 URL（多模态生成 API 返回 output.audio.url）
    const audioUrl = data?.output?.audio?.url;
    if (!audioUrl) {
      console.error("API 响应:", JSON.stringify(data, null, 2));
      throw new Error("API 返回无 audio.url");
    }

    console.log(`获取音频 URL: ${audioUrl}`);

    // 3. 下载音频并保存为本地 MP3
    const audioResponse = await fetch(audioUrl);
    const audioBuffer = Buffer.from(await audioResponse.arrayBuffer());
    fs.writeFileSync(OUTPUT_FILE, audioBuffer);

    console.log(`已保存 MP3 至: ${OUTPUT_FILE}`);
    console.log(`音频链接有效期：24 小时（过期需重新调用 API）`);
  } catch (error) {
    console.error("合成失败:", error.message);
    throw error;
  }
}

// 执行
generateTTS();
