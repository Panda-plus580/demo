// 向量嵌入模块：使用 DashScope（阿里云百炼）生成文本向量
// 通过 @langchain/openai 的 OpenAIEmbeddings 封装，更简洁的 API
import { OpenAIEmbeddings } from "@langchain/openai";
import { config } from "./config.js";

/**
 * LangChain OpenAIEmbeddings 实例，指向 DashScope 兼容端点
 */
const embeddings = new OpenAIEmbeddings({
  openAIApiKey: config.embedding.apiKey,
  modelName: config.embedding.model,
  dimensions: config.embedding.dimensions,
  configuration: {
    baseURL: config.embedding.baseURL,
  },
});

/**
 * 将单条文本转换为向量
 * @param {string} text - 输入文本
 * @returns {Promise<number[]>} 向量数组
 */
export async function embedText(text) {
  return embeddings.embedQuery(text);
}

/**
 * 批量将多条文本转换为向量
 * @param {string[]} texts - 输入文本数组
 * @returns {Promise<number[][]>} 向量数组的数组
 */
export async function embedTexts(texts) {
  return embeddings.embedDocuments(texts);
}
