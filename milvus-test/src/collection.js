// 集合管理模块：创建、检查、删除 Milvus Collection
import { DataType, MetricType, IndexType } from "@zilliz/milvus2-sdk-node";
import { milvusClient } from "./client.js";
import { config } from "./config.js";

const { name, dimension, primaryField, vectorField } = config.collection;

/**
 * 检查集合是否已存在
 * @returns {Promise<boolean>}
 */
export async function hasCollection() {
  const result = await milvusClient.hasCollection({
    collection_name: name,
  });
  return result.value === true;
}

/**
 * 创建 Collection（定义 Schema + 创建索引 + 加载到内存）
 *
 * Schema 设计：
 * - id: VARCHAR 类型主键（字符串 ID）
 * - title: VARCHAR 类型（文章标题）
 * - content: VARCHAR 类型（文章内容，原文存储）
 * - embedding: FloatVector 类型（1024 维文本向量）
 */
export async function createCollection() {
  const exists = await hasCollection();
  if (exists) {
    console.log(`[collection] 集合 "${name}" 已存在，跳过创建`);
    return;
  }

  console.log(`[collection] 正在创建集合 "${name}"...`);

  // 1. 创建 Collection，定义字段 Schema
  await milvusClient.createCollection({
    collection_name: name,
    fields: [
      {
        name: primaryField,
        data_type: DataType.VarChar,
        is_primary_key: true,
        type_params: { max_length: "64" },
        autoID: false,
      },
      {
        name: "title",
        data_type: DataType.VarChar,
        type_params: { max_length: "512" },
      },
      {
        name: "content",
        data_type: DataType.VarChar,
        type_params: { max_length: "65535" },
      },
      {
        name: vectorField,
        data_type: DataType.FloatVector,
        type_params: { dim: String(dimension) },
      },
    ],
    description: "文章向量存储示例集合",
    enable_dynamic_field: true,
  });

  console.log(`[collection] 集合 "${name}" 创建成功`);

  // 2. 为向量字段创建索引（IVF_FLAT 适合中等规模数据）
  console.log(`[collection] 正在为 "${vectorField}" 字段创建索引...`);
  await milvusClient.createIndex({
    collection_name: name,
    field_name: vectorField,
    index_type: IndexType.IVF_FLAT,
    metric_type: MetricType.COSINE,
    params: { nlist: 128 },
  });
  console.log(`[collection] 索引创建成功`);

  // 3. 加载 Collection 到内存（搜索前必须加载）
  console.log(`[collection] 正在加载集合到内存...`);
  await milvusClient.loadCollectionSync({
    collection_name: name,
  });
  console.log(`[collection] 集合 "${name}" 已加载到内存，可进行搜索`);
}

/**
 * 释放集合（从内存中卸载）
 */
export async function releaseCollection() {
  console.log(`[collection] 正在释放集合 "${name}"...`);
  await milvusClient.releaseCollection({
    collection_name: name,
  });
  console.log(`[collection] 集合 "${name}" 已释放`);
}

/**
 * 删除集合
 */
export async function dropCollection() {
  const exists = await hasCollection();
  if (!exists) {
    console.log(`[collection] 集合 "${name}" 不存在，无需删除`);
    return;
  }
  console.log(`[collection] 正在删除集合 "${name}"...`);
  await milvusClient.dropCollection({
    collection_name: name,
  });
  console.log(`[collection] 集合 "${name}" 已删除`);
}
