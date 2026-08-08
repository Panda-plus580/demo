// 主入口：演示 Milvus 向量数据库的完整 CRUD 操作
import {
  createCollection,
  hasCollection,
  dropCollection,
} from "./collection.js";
import { insertArticles } from "./create.js";
import { searchByText, queryByIds, countArticles } from "./read.js";
import { upsertArticles } from "./update.js";
import { deleteByIds, deleteByFilter } from "./delete.js";

// 示例数据：模拟文章库
const sampleArticles = [
  {
    id: "article-001",
    title: "深入理解 Node.js 事件循环",
    content:
      "Node.js 的事件循环是其非阻塞 I/O 模型的核心。它允许 Node.js 在单个线程上执行异步操作，通过 libuv 库实现。事件循环分为多个阶段：timers、pending callbacks、idle/prepare、poll、check、close callbacks。理解这些阶段对于编写高性能 Node.js 应用至关重要。",
  },
  {
    id: "article-002",
    title: "向量数据库入门：从零开始学习 Milvus",
    content:
      "向量数据库是专门用于存储和检索高维向量的数据库系统。Milvus 是一个开源的向量数据库，支持万亿级向量数据的管理。它广泛应用于推荐系统、图像检索、自然语言处理等领域。Milvus 支持多种索引类型，如 IVF_FLAT、HNSW 等，可以高效地进行近似最近邻搜索（ANN）。",
  },
  {
    id: "article-003",
    title: "Python 机器学习实战：用 Scikit-learn 构建分类器",
    content:
      "Scikit-learn 是 Python 生态中最流行的机器学习库之一。它提供了丰富的分类、回归、聚类算法。本文将通过一个完整的示例，演示如何使用 Scikit-learn 构建一个文本分类器。首先进行数据预处理，然后选择合适的特征提取方法（如 TF-IDF），最后训练和评估模型。",
  },
  {
    id: "article-004",
    title: "Docker 容器化部署最佳实践",
    content:
      "Docker 已成为现代应用部署的标准工具。本文介绍 Docker 容器化的最佳实践：使用多阶段构建减小镜像体积、合理使用 .dockerignore 文件、避免以 root 用户运行容器、使用健康检查确保服务可用性、配置日志驱动便于监控。此外还将讨论 Docker Compose 管理多容器应用。",
  },
  {
    id: "article-005",
    title: "TypeScript 高级类型技巧",
    content:
      "TypeScript 的类型系统非常强大，掌握高级类型技巧可以大幅提升代码质量。本文涵盖条件类型（Conditional Types）、映射类型（Mapped Types）、模板字面量类型（Template Literal Types）、类型推断（infer）等进阶话题。通过这些技巧，你可以编写更安全、更灵活的 TypeScript 代码。",
  },
];

// 主流程：依次演示 CRUD 操作
async function main() {
  console.log("Milvus 向量数据库 CRUD 操作示例");

  // 0. 准备：创建集合
  console.log("========== Step 0: 创建集合 ==========");
  await createCollection();

  // 1. CREATE：插入数据
  console.log("\n========== Step 1: CREATE 插入数据 ==========");
  await insertArticles(sampleArticles);

  // 查看数据量
  await countArticles();

  // 2. READ：向量搜索
  console.log("\n========== Step 2: READ 向量相似度搜索 ==========");
  await searchByText("如何在 Node.js 中处理异步操作？", { topK: 3 });
  await searchByText("什么是向量数据库？", { topK: 2 });
  await searchByText("Docker 部署相关的内容", { topK: 2 });

  // 3. READ：按 ID 精确查询
  console.log("\n========== Step 3: READ 按 ID 查询 ==========");
  await queryByIds(["article-001", "article-003"]);

  // 4. UPDATE：Upsert 更新数据
  console.log("\n========== Step 4: UPDATE Upsert 更新 ==========");
  // 修改 article-002 的内容（ID 相同，内容更新）
  await upsertArticles([
    {
      id: "article-002",
      title: "向量数据库入门：从零开始学习 Milvus（修订版）",
      content:
        "向量数据库是 AI 时代的基础设施。Milvus 作为开源向量数据库的领导者，提供了高性能的向量存储和检索能力。本文从零开始讲解 Milvus 的核心概念：Collection、Partition、Index、Entity。同时对比 Faiss、Weaviate、Pinecone 等竞品，帮助你选择最适合的向量数据库方案。",
    },
  ]);

  // 验证更新：搜索验证新内容已生效
  await searchByText("Milvus 和其他向量数据库的对比", { topK: 2 });

  // 5. DELETE：按 ID 删除
  console.log("\n========== Step 5: DELETE 按 ID 删除 ==========");
  await deleteByIds(["article-005"]);

  // 验证删除后搜索结果中不再包含 article-005
  await searchByText("TypeScript 类型系统", { topK: 3 });

  // 查看最终数据量
  await countArticles();

  // 6. DELETE：按条件删除
  console.log("\n========== Step 6: DELETE 按条件删除 ==========");
  await deleteByFilter('id == "article-004"');

  // 最终查看
  await countArticles();

  console.log("\nCRUD 操作演示完成！🚀");
}

// 运行主流程
main().catch((err) => {
  console.error("❌ 运行出错:", err.message);
  console.error(err);
  process.exit(1);
});
