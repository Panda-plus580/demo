import * as fs from "fs";
import { Milvus } from "@langchain/community/vectorstores/milvus";
import {
  MilvusClient,
  DataType,
  MetricType,
  IndexType,
} from "@zilliz/milvus2-sdk-node";
import { OpenAIEmbeddings, ChatOpenAI } from "@langchain/openai";
import { RecursiveCharacterTextSplitter } from "@langchain/classic/text_splitter";
import { ChatPromptTemplate } from "@langchain/core/prompts";
import { StringOutputParser } from "@langchain/core/output_parsers";
import express, { type Response } from "express";
import "dotenv/config";

// 配置常量
const COLLECTION_NAME = "hongloumeng";
const MILVUS_ADDRESS = "localhost:19530";
const PORT = 18000;

// 1. 初始化 Embeddings 模型
const dashscopeApiKey = process.env.DASHSCOPE_API_KEY;
if (!dashscopeApiKey) {
  throw new Error("Missing DASHSCOPE_API_KEY in .env");
}

const embeddings = new OpenAIEmbeddings({
  apiKey: dashscopeApiKey,
  model: "text-embedding-v4",
  batchSize: 10,
  configuration: {
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  },
});

// 2. 初始化 LLM
const model = new ChatOpenAI({
  apiKey: dashscopeApiKey,
  model: "qwen-plus",
  temperature: 0.7,
  configuration: {
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  },
});

// 2.1 RAG 提示词模板（模块级，供流式链复用）
const prompt = ChatPromptTemplate.fromTemplate(`
  你是一个精通《红楼梦》的文学助手。请根据以下检索到的上下文信息回答用户的问题。
  如果上下文中没有相关信息，请直接说明你不知道，不要编造内容。

  上下文信息：
  {context}

  用户问题：{input}
  回答：
`);

// 3. Milvus 原生客户端（管理操作）
const milvusClient = new MilvusClient({
  address: MILVUS_ADDRESS,
  database: "default",
  timeout: 30000,
});

let vectorStore: Milvus;

// 4. 集合管理

/**
 * 确保 Milvus 集合存在（含 Schema + 索引），并加载到内存
 *
 * Schema 设计（与 LangChain Milvus 包装器兼容）：
 * - id:      VarChar 主键（LangChain 自动生成 UUID）
 * - text:    VarChar 原文（LangChain 默认文本字段名）
 * - vector:  FloatVector(1024) 向量（LangChain 默认向量字段名）
 */
async function ensureCollection(): Promise<void> {
  const result = await milvusClient.hasCollection({
    collection_name: COLLECTION_NAME,
  });

  if (result.value) {
    console.log(`Milvus 集合 "${COLLECTION_NAME}" 已存在`);
    // 确保集合已加载到内存（服务重启后需要重新加载）
    await milvusClient.loadCollectionSync({
      collection_name: COLLECTION_NAME,
    });
    console.log(`集合 "${COLLECTION_NAME}" 已加载到内存`);
    return;
  }

  console.log(`正在创建 Milvus 集合 "${COLLECTION_NAME}"...`);

  // 4.1 创建 Collection Schema
  await milvusClient.createCollection({
    collection_name: COLLECTION_NAME,
    fields: [
      {
        name: "id",
        data_type: DataType.VarChar,
        is_primary_key: true,
        type_params: { max_length: "100" },
        autoID: false,
      },
      {
        name: "text",
        data_type: DataType.VarChar,
        type_params: { max_length: "65535" },
      },
      {
        name: "vector",
        data_type: DataType.FloatVector,
        type_params: { dim: "1024" }, // text-embedding-v4 输出 1024 维
      },
    ],
    description: "红楼梦文本向量存储",
  });
  console.log(`集合 "${COLLECTION_NAME}" 创建成功`);

  // 4.2 创建 IVF_FLAT 索引（适合十万级数据，余弦相似度）
  await milvusClient.createIndex({
    collection_name: COLLECTION_NAME,
    field_name: "vector",
    index_type: IndexType.IVF_FLAT,
    metric_type: MetricType.COSINE,
    params: { nlist: 128 },
  });
  console.log("索引创建成功（IVF_FLAT + COSINE）");

  // 4.3 加载集合到内存
  await milvusClient.loadCollectionSync({
    collection_name: COLLECTION_NAME,
  });
  console.log("集合已加载到内存");
}

// 5. 数据入库

async function ingestData() {
  console.log("正在读取《红楼梦》文本...");
  const rawText = fs.readFileSync("./data/hong.txt", "utf-8");

  console.log("正在进行文本分割...");
  const textSplitter = new RecursiveCharacterTextSplitter({
    chunkSize: 1000,
    chunkOverlap: 200,
    separators: ["\n\n", "\n", "。", "！", "？", "；", "，", " "],
  });

  const docs = await textSplitter.createDocuments([rawText]);

  // LangChain Milvus 包装器要求每个文档的 metadata.text 存在（用于存入 Milvus 的 text 字段）
  // autoId=false 模式下还需要 metadata.id 作为主键
  for (let i = 0; i < docs.length; i++) {
    docs[i].metadata.text = docs[i].pageContent;
    docs[i].metadata.id = `hlm_${String(i + 1).padStart(6, "0")}`;
  }

  console.log(`分割完成，共 ${docs.length} 个片段。正在存入向量数据库...`);

  // LangChain Milvus 包装器自动生成向量并插入
  await vectorStore.addDocuments(docs);

  // 刷新数据确保持久化（Milvus 默认每秒自动刷新，这里手动触发保证立即可搜索）
  await milvusClient.flushSync({
    collection_names: [COLLECTION_NAME],
  });
  console.log("数据已刷新到磁盘");

  console.log("数据入库完成！");
}

// 6. 工具函数

function formatContext(docs: Array<{ pageContent: string }>): string {
  return docs
    .map((doc) => doc.pageContent)
    .join("\n\n");
}

function writeSseEvent(res: Response, event: string, data: unknown): void {
  res.write(`event: ${event}\n`);
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

// 6.1 流式 RAG 问答（LCEL pipe + stream）
async function* streamChatWithHongLouMeng(query: string): AsyncGenerator<string> {
  console.log(`\n🔍 正在检索关于 "${query}" 的内容...`);
  // 1. 搜出最相关的 4 段原文
  const retriever = vectorStore.asRetriever({
    searchType: "similarity",
    k: 4,
  });
  const docs = await retriever.invoke(query);

  // 2. 组装提示词：告诉大模型它是谁 + 给它参考资料 + 用户问题
  const chain = prompt.pipe(model).pipe(new StringOutputParser());

  // 3. 流式生成：每出一个字就 yield 出去
  const stream = await chain.stream({
    context: formatContext(docs),
    input: query,
  });

  for await (const chunk of stream) {
    if (chunk) {
      yield chunk; // "贾" → "宝玉" → "的外貌" → ...
    }
  }
}

// 7. 主入口

async function main(): Promise<void> {
  console.log("正在连接 Milvus（address: " + MILVUS_ADDRESS + "）...");

  // 7.1 确保集合存在并已加载
  await ensureCollection();

  // 7.2 使用 LangChain 的 Milvus 包装器连接已有集合
  // 显式指定字段名和搜索参数，必须与集合的实际索引一致
  vectorStore = await Milvus.fromExistingCollection(embeddings, {
    collectionName: COLLECTION_NAME,
    clientConfig: {
      address: MILVUS_ADDRESS,
    },
    textField: "text",
    vectorField: "vector",
    primaryField: "id",
    autoId: false,
    indexCreateOptions: {
      metric_type: "COSINE",
      index_type: "IVF_FLAT",
      params: { nlist: 128 },
    },
  });

  console.log("Vector store ready:", Boolean(vectorStore));

  // 7.3 命令行模式：入库
  const command = process.argv[2];
  if (command === "ingest") {
    await ingestData();
    return;
  }

  // 7.4 HTTP API 服务模式
  const app = express();
  app.use(express.json());
  app.use((req, res, next) => {
    res.header("Access-Control-Allow-Origin", "*");
    res.header("Access-Control-Allow-Methods", "POST,OPTIONS");
    res.header("Access-Control-Allow-Headers", "Content-Type,Accept");
    if (req.method === "OPTIONS") {
      res.sendStatus(204);
      return;
    }
    next();
  });

  app.post("/hongloumeng/chat", async (req, res) => {
    const questionParam = req.body?.question;
    const question = typeof questionParam === "string" ? questionParam.trim() : "";

    if (!question) {
      res.status(400).json({
        error: "Missing body parameter `question`",
      });
      return;
    }

    try {
      res.status(200);
      res.setHeader("Content-Type", "text/event-stream; charset=utf-8");
      res.setHeader("Cache-Control", "no-cache, no-transform");
      res.setHeader("Connection", "keep-alive");
      res.setHeader("X-Accel-Buffering", "no");
      res.flushHeaders?.();

      let answer = "";
      let clientClosed = false;
      res.on("close", () => {
        clientClosed = true;
      });

      writeSseEvent(res, "start", { question });

      for await (const chunk of streamChatWithHongLouMeng(question)) {
        if (clientClosed) {
          break;
        }
        answer += chunk;
        writeSseEvent(res, "chunk", { content: chunk });
      }

      if (!clientClosed) {
        writeSseEvent(res, "done", { question, answer });
      }
    } catch (error) {
      console.error("chat api failed:", error);
      if (!res.headersSent) {
        res.status(500).json({
          error: "Failed to generate answer",
        });
        return;
      }
      writeSseEvent(res, "error", {
        error: "Failed to generate answer",
      });
    } finally {
      if (!res.writableEnded) {
        res.end();
      }
    }
  });

  app.listen(PORT, () => {
    console.log(`🚀 API server running at http://localhost:${PORT}`);
    console.log(
      'Example: POST /hongloumeng/chat with body {"question":"贾宝玉的外貌如何？"}',
    );
  });
}

main().catch((error) => {
  console.error("启动失败:", error);
  process.exit(1);
});
