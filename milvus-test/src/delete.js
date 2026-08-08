// 删（Delete）：按 ID 删除、按过滤条件删除
import { milvusClient } from "./client.js";
import { config } from "./config.js";

const { name } = config.collection;

/**
 * 按 ID 删除指定的数据
 *
 * @param {string[]} ids - 要删除的 ID 数组
 * @returns {Promise<Object>}
 */
export async function deleteByIds(ids) {
  console.log(`\n[delete] 按 ID 删除: [${ids.join(", ")}]`);

  const result = await milvusClient.delete({
    collection_name: name,
    ids: ids,
  });

  console.log(`[delete] 删除完成`);

  // 刷新使删除生效
  await milvusClient.flushSync({ collection_names: [name] });

  return result;
}

/**
 * 按过滤表达式删除数据
 *
 * @param {string} filter - 过滤表达式，如 'id like "article-%"'
 * @returns {Promise<Object>}
 */
export async function deleteByFilter(filter) {
  console.log(`\n[delete] 按条件删除: ${filter}`);

  const result = await milvusClient.delete({
    collection_name: name,
    filter: filter,
  });

  console.log(`[delete] 条件删除完成`);

  // 刷新使删除生效
  await milvusClient.flushSync({ collection_names: [name] });

  return result;
}

/**
 * 删除集合中所有数据（通过清空表达式）
 */
export async function deleteAll() {
  console.log(`\n[delete] 清空集合 "${name}" 的所有数据...`);

  const result = await milvusClient.delete({
    collection_name: name,
    filter: 'id != ""', // 匹配所有非空 id
  });

  console.log(`[delete] 清空完成`);

  // 刷新使删除生效
  await milvusClient.flushSync({ collection_names: [name] });

  return result;
}
