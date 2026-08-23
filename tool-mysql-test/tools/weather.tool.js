import { tool } from "@langchain/core/tools";
import { z } from "zod";

/** 模拟的天气数据库(方便演示,不用真的调外部 API) */
const WEATHER_MOCK = {
  北京: { weather: "晴", temperature: 26, wind: "东北风 2 级" },
  上海: { weather: "小雨", temperature: 24, wind: "东南风 3 级" },
  广州: { weather: "多云", temperature: 30, wind: "南风 2 级" },
  深圳: { weather: "雷阵雨", temperature: 29, wind: "南风 3 级" },
  杭州: { weather: "阴", temperature: 23, wind: "东风 2 级" },
  成都: { weather: "晴", temperature: 22, wind: "微风" },
  武汉: { weather: "晴转多云", temperature: 27, wind: "北风 2 级" },
};

/**
 * 天气查询 Tool
 * 真实场景中可在此处调用外部天气 API(如和风天气、高德天气等)。
 */
const getWeatherTool = tool(
  async ({ city }) => {
    console.log(`☁️ 正在查询 ${city} 的天气...`);
    await new Promise((r) => setTimeout(r, 200)); // 模拟网络延迟

    const data = WEATHER_MOCK[city];
    if (!data) {
      return JSON.stringify({
        message: `暂不支持查询 ${city} 的天气,支持的城市场景有:${Object.keys(
          WEATHER_MOCK
        ).join("、")}`,
      });
    }
    return JSON.stringify({ city, ...data });
  },
  {
    name: "get_weather",
    description: "查询指定城市的当前天气情况,返回天气、温度、风力等信息。",
    schema: z.object({
      city: z.string().describe("城市名称,例如:北京、上海、广州"),
    }),
  }
);

export const weatherTools = [getWeatherTool];
