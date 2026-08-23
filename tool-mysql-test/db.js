import mysql from "mysql2/promise";
import dotenv from "dotenv";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
dotenv.config({ path: path.join(__dirname, ".env") });

// 数据库连接配置(请按本地环境修改)
export const dbConfig = {
  host: process.env.DB_HOST || "localhost",
  port: Number(process.env.DB_PORT) || 3306,
  user: process.env.DB_USER || "root",
  password: process.env.DB_PASSWORD || "admin",
  database: process.env.DB_NAME || "mysql_test",
  waitForConnections: true,
  connectionLimit: 10,
  queueLimit: 0,
};

// 创建连接池(支持 Promise 写法)
export const pool = mysql.createPool(dbConfig);

/** 测试数据库连接是否可用 */
export async function checkDbConnection() {
  await pool.query("SELECT 1");
}
