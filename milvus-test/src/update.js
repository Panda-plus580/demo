// 改（Update）：使用 upsert 更新/插入数据
import { milvusClient } from "./client.js";
import { embedText } from "./embedding.js";
import { config } from "./config.js";

const { name, primaryField, vectorField } = config.collection;

/**
 * Upsert 操作：如果 ID 已存在则更新，否则插入
 *
 * @param {Array<{id: string, title: string, content: string}>} articles
 * @returns {Promise<Object>}
 */
export async function upsertArticles(articles) {
  console.log(`\n[update] 正在 upsert ${articles.length} 篇文章...`);

  // 生成向量
  const embeddings = [];
  for (const article of articles) {
    const vec = await embedText(article.content);
    embeddings.push(vec);
  }

  // 组装数据
  const rows = articles.map((article, i) => ({
    [primaryField]: article.id,
    title: article.title,
    content: article.content,
    [vectorField]: embeddings[i],
  }));

  // 执行 upsert
  const result = await milvusClient.upsert({
    collection_name: name,
    data: rows,
  });

  console.log(`[update] Upsert 完成，upsert_cnt: ${result.upsert_cnt}`);
  console.log(`[update] 涉及的 ID:`, result.IDs);

  // 刷新数据确保持久化并可被搜索
  await milvusClient.flushSync({ collection_names: [name] });

  return result;
}

/**
 * 更新单篇文章（通过 ID 标识，存在则更新，不存在则插入）
 */
export async function upsertOneArticle(article) {
  return upsertArticles([article]);
}
