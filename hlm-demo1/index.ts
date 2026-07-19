import * as fs from "fs";
import { Chroma } from "@langchain/community/vectorstores/chroma";
import { OpenAIEmbeddings, ChatOpenAI } from "@langchain/openai";
import { RecursiveCharacterTextSplitter } from "@langchain/classic/text_splitter";
import { ChatPromptTemplate } from "@langchain/core/prompts";
import { StringOutputParser } from "@langchain/core/output_parsers";
import { createStuffDocumentsChain } from "@langchain/classic/chains/combine_documents";
import { createRetrievalChain } from "@langchain/classic/chains/retrieval";
import express from "express";
import "dotenv/config";

// 1. 初始化 Embeddings 模型 (使用 text-embedding-v4)
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

// 2. 初始化 LLM (使用 qwen-plus)
const model = new ChatOpenAI({
  apiKey: dashscopeApiKey,
  model: "qwen-plus",
  temperature: 0.7,
  configuration: {
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  },
});

let vectorStore:Chroma;
const PORT = 18000;

async function ingestData() {
  console.log("正在读取《红楼梦》文本...");
  const rawText = fs.readFileSync("./data/hong.txt", "utf-8");

  console.log("正在进行文本分割...");
  const textSplitter = new RecursiveCharacterTextSplitter({
    chunkSize: 1000, // 每个块的大小
    chunkOverlap: 200, // 块之间的重叠，保持上下文连贯性
    separators: ["\n\n", "\n", "。", "！", "？", "；", "，", " "], // 针对中文优化的分隔符
  });

  const docs = await textSplitter.createDocuments([rawText]);

  console.log(`分割完成，共 ${docs.length} 个片段。正在存入向量数据库...`);
  
  // 将文档添加到现有的 Chroma 集合中
  await vectorStore.addDocuments(docs);
  
  console.log("数据入库完成！");
}


async function chatWithHongLouMeng(query: string) {
  console.log(`\n正在检索关于 "${query}" 的内容...`);

  // 设置检索器，获取最相关的 4 个片段
  const retriever = vectorStore.asRetriever({
    searchType: "similarity",
    k: 4,
  });

  // 定义提示词模板
  const prompt = ChatPromptTemplate.fromTemplate(`
    你是一个精通《红楼梦》的文学助手。请根据以下检索到的上下文信息回答用户的问题。
    如果上下文中没有相关信息，请直接说明你不知道，不要编造内容。

    上下文信息：
    {context}

    用户问题：{input}
    回答：
  `);

  // 创建文档组合链
  const combineDocsChain = await createStuffDocumentsChain({
    llm: model,
    prompt,
    outputParser: new StringOutputParser(),
  });

  // 创建检索链
  const chain = await createRetrievalChain({
    retriever,
    combineDocsChain,
  });

  const response = await chain.invoke({ input: query });
  return response.answer;
}

async function main(): Promise<void> {
  // 注意：LangChainJS 的 Chroma 类默认连接 localhost:8000
  vectorStore = await Chroma.fromExistingCollection(embeddings, {
    collectionName: "hongloumeng",
    url: "http://localhost:8000",
  });

  console.log("Vector store ready:", Boolean(vectorStore));
  const command = process.argv[2];
  if (command === "ingest") {
    await ingestData();
    return;
  }

  const app = express();
  app.use(express.json());
  app.use((_, res, next) => {
    res.header("Access-Control-Allow-Origin", "*");
    res.header("Access-Control-Allow-Methods", "POST,OPTIONS");
    res.header("Access-Control-Allow-Headers", "Content-Type");
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
      const answer = await chatWithHongLouMeng(question);
      res.json({ question, answer });
    } catch (error) {
      console.error("chat api failed:", error);
      res.status(500).json({
        error: "Failed to generate answer",
      });
    }
  });

  app.listen(PORT, () => {
    console.log(`API server running at http://localhost:${PORT}`);
    console.log(
      'Example: POST /hongloumeng/chat with body {"question":"贾宝玉的外貌如何？"}',
    );
  });
}


main().catch((error) => {
  console.error("qwen-plus connectivity test failed:", error);
  process.exit(1);
});
