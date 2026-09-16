import { McpServer } from "@modelcontextprotocol/server";
import { StdioServerTransport } from "@modelcontextprotocol/server/stdio";
import { z } from "zod";

// 1. 创建 MCP Server 实例，name/version 会通过 initialize 响应暴露给客户端
const server = new McpServer({ name: "calculator", version: "1.0.0" });

// 2. 抽一个公共的入参 schema：a、b 都是数字
const binaryInput = z.object({
  a: z.number().describe("第一个操作数"),
  b: z.number().describe("第二个操作数"),
});

// 3. 抽一个统一的返回格式：MCP 工具必须返回 content 数组
const text = (t) => ({ content: [{ type: "text", text: String(t) }] });

// 4. 注册加法工具
server.registerTool(
  "add",
  {
    title: "加法",
    description: "计算两个数字的和 a + b",
    inputSchema: binaryInput,
  },
  async ({ a, b }) => text(a + b)
);

// 5. 注册减法工具
server.registerTool(
  "subtract",
  {
    title: "减法",
    description: "计算两个数字的差 a - b",
    inputSchema: binaryInput,
  },
  async ({ a, b }) => text(a - b)
);

// 6. 注册乘法工具
server.registerTool(
  "multiply",
  {
    title: "乘法",
    description: "计算两个数字的积 a * b",
    inputSchema: binaryInput,
  },
  async ({ a, b }) => text(a * b)
);

// 7. 注册除法工具（需要处理除零）
server.registerTool(
  "divide",
  {
    title: "除法",
    description: "计算两个数字的商 a / b，b 不能为 0",
    inputSchema: binaryInput,
  },
  async ({ a, b }) => {
    if (b === 0) {
      return {
        content: [{ type: "text", text: "错误：除数不能为 0" }],
        isError: true,
      };
    }
    return text(a / b);
  }
);

// 8. 使用 stdio 传输启动服务
const transport = new StdioServerTransport();
await server.connect(transport);

// 注意：stdio 场景下日志必须走 stderr，绝不能 console.log
console.error("Calculator MCP Server running on stdio");
