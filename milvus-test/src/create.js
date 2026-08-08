// 增（Create）：向 Milvus 插入向量数据
import { milvusClient } from "./client.js";
import { embedTexts } from "./embedding.js";
import { config } from "./config.js";

const { name, primaryField, vectorField } = config.collection;

/**
 * 插入一批文章数据到 Milvus
 * @param {Array<{id: string, title: string, content: string}>} articles - 文章数据
 * @returns {Promise<Object>} 插入结果
 */
export async function insertArticles(articles) {
  console.log(`\n[create] 正在生成 ${articles.length} 篇文章的向量嵌入...`);

  // 1. 提取所有文章内容，批量生成向量
  const contents = articles.map((a) => a.content);
  const embeddings = await embedTexts(contents);
  console.log(`[create] 向量嵌入生成完毕，维度: ${embeddings[0].length}`);

  // 2. 组装插入数据
  const rows = articles.map((article, i) => ({
    [primaryField]: article.id,
    title: article.title,
    content: article.content,
    [vectorField]: embeddings[i],
  }));

  console.log(`[create] 正在插入 ${rows.length} 条数据...`);

  // 3. 执行插入
  const result = await milvusClient.insert({
    collection_name: name,
    data: rows,
  });

  console.log(`[create] 插入成功，插入条数: ${result.insert_cnt}`);
  console.log(`[create] 插入的 ID 列表:`, result.IDs);

  // 4. 刷新数据确保持久化并可被搜索
  await milvusClient.flushSync({ collection_names: [name] });

  return result;
}

/**
 * 单条插入
 */
export async function insertOneArticle(article) {
  return insertArticles([article]);
}
