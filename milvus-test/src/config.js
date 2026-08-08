// 配置模块：集中管理 Milvus 连接、嵌入模型等配置
import "dotenv/config";

export const config = {
  // Milvus 向量数据库连接地址
  milvus: {
    address: "localhost:19530",
    // 数据库名（默认 default）
    database: "default",
  },

  // 嵌入模型配置（阿里云百炼 DashScope，兼容 OpenAI API）
  embedding: {
    // DashScope OpenAI 兼容接口
    apiKey: process.env.DASHSCOPE_API_KEY,
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    // text-embedding-v4 是百炼最新的嵌入模型，1024 维
    model: "text-embedding-v4",
    dimensions: 1024,
  },

  // 集合配置
  collection: {
    name: "demo_articles",
    // 向量维度，必须与嵌入模型输出维度一致
    dimension: 1024,
    // 主键字段名
    primaryField: "id",
    // 向量字段名
    vectorField: "embedding",
  },
};
