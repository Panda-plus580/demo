// Milvus 客户端模块：创建并导出单例 MilvusClient
import { MilvusClient } from "@zilliz/milvus2-sdk-node";
import { config } from "./config.js";

/**
 * 创建 Milvus 客户端实例（单例）
 * 连接地址为 localhost:19530（由 Docker Compose 中的 milvus-standalone 服务暴露）
 */
export const milvusClient = new MilvusClient({
  address: config.milvus.address,
  database: config.milvus.database,
  // 连接超时时间（毫秒）
  timeout: 30000,
});

console.log(`[client] Milvus 客户端已初始化，地址: ${config.milvus.address}`);
