import mysql from "mysql2/promise";
import { pool, dbConfig } from "./db.js";

const CREATE_TABLE_SQL = `
CREATE TABLE IF NOT EXISTS users (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(50) NOT NULL COMMENT '姓名',
  email VARCHAR(100) NOT NULL UNIQUE COMMENT '邮箱',
  age INT DEFAULT 0 COMMENT '年龄',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户表';
`;

// 预置一些示例数据,方便演示查询
const SEED_USERS = [
  ["张三", "zhangsan@test.com", 28],
  ["李四", "lisi@test.com", 24],
  ["王五", "wangwu@test.com", 32],
];

async function init() {
  // 先连接(不带 database)创建数据库
  const conn = await mysql.createConnection({
    host: dbConfig.host,
    port: dbConfig.port,
    user: dbConfig.user,
    password: dbConfig.password,
  });

  await conn.query(
    `CREATE DATABASE IF NOT EXISTS \`${dbConfig.database}\` DEFAULT CHARACTER SET utf8mb4`
  );
  console.log(`✅ 数据库 ${dbConfig.database} 已就绪`);
  await conn.end();

  // 再通过连接池建表
  await pool.query(CREATE_TABLE_SQL);
  console.log("✅ users 表已就绪");

  // 表为空时插入示例数据
  const [[{ total }]] = await pool.query("SELECT COUNT(*) AS total FROM users");
  if (total === 0) {
    await pool.query("INSERT INTO users (name, email, age) VALUES ?", [
      SEED_USERS,
    ]);
    console.log(`✅ 已插入 ${SEED_USERS.length} 条示例数据`);
  }
}

init()
  .then(() => process.exit(0))
  .catch((err) => {
    console.error("❌ 初始化失败:", err.message);
    process.exit(1);
  });
