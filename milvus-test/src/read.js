// 查（Read）：向量相似度搜索 + 标量过滤查询
import { milvusClient } from "./client.js";
import { embedText } from "./embedding.js";
import { config } from "./config.js";

const { name, primaryField, vectorField } = config.collection;

/**
 * 向量相似度搜索：根据查询文本，找到最相似的 Top-K 篇文章
 *
 * @param {string} queryText - 查询文本
 * @param {Object} options
 * @param {number} [options.topK=3] - 返回的最相似结果数
 * @param {string} [options.filter] - 可选的标量过滤表达式
 * @returns {Promise<Array>} 搜索结果
 */
export async function searchByText(queryText, { topK = 3, filter } = {}) {
  console.log(`\n[read] 搜索查询: "${queryText}"`);

  // 1. 将查询文本转换为向量
  const queryVector = await embedText(queryText);

  // 2. 构建搜索参数
  const searchParams = {
    collection_name: name,
    data: queryVector,
    anns_field: vectorField,
    limit: topK,
    output_fields: ["id", "title", "content"],
    params: { nprobe: 16 },
  };

  // 可选：添加标量过滤
  if (filter) {
    searchParams.filter = filter;
    console.log(`[read] 过滤条件: ${filter}`);
  }

  // 3. 执行搜索
  const result = await milvusClient.search(searchParams);

  // 4. 格式化输出
  const hits = result.results.map((hit, i) => ({
    rank: i + 1,
    id: hit.id,
    title: hit.title,
    content: hit.content,
    score: hit.score,
  }));

  console.log(`[read] 搜索到 ${hits.length} 条结果:`);
  hits.forEach((h) => {
    console.log(
      `  #${h.rank} [${h.id}] ${h.title} (相似度: ${h.score.toFixed(4)})`
    );
  });

  return hits;
}

/**
 * 根据 ID 精确查询（标量查询，非向量搜索）
 *
 * @param {string[]} ids - 要查询的文章 ID 数组
 * @returns {Promise<Array>} 查询结果
 */
export async function queryByIds(ids) {
  console.log(`\n[read] 按 ID 查询: [${ids.join(", ")}]`);

  const result = await milvusClient.query({
    collection_name: name,
    ids: ids,
    output_fields: ["id", "title", "content"],
  });

  console.log(`[read] 查询到 ${result.data.length} 条记录:`);
  result.data.forEach((row) => {
    console.log(`  [${row.id}] ${row.title}`);
  });

  return result.data;
}

/**
 * 使用过滤表达式查询（标量过滤，不涉及向量搜索）
 *
 * @param {string} filter - 过滤表达式，如 'id like "article-%"'
 * @param {Object} options
 * @param {number} [options.limit=10] - 返回数量
 * @returns {Promise<Array>} 查询结果
 */
export async function queryByFilter(filter, { limit = 10 } = {}) {
  console.log(`\n[read] 过滤查询: ${filter}`);

  const result = await milvusClient.query({
    collection_name: name,
    filter: filter,
    output_fields: ["id", "title", "content"],
    limit: limit,
  });

  console.log(`[read] 查询到 ${result.data.length} 条记录`);
  return result.data;
}

/**
 * 统计集合中的数据总数
 * @returns {Promise<number>}
 */
export async function countArticles() {
  const result = await milvusClient.count({
    collection_name: name,
  });
  console.log(`[read] 集合 "${name}" 共有 ${result.data} 条数据`);
  return result.data;
}
