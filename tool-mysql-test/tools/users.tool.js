import { tool } from "@langchain/core/tools";
import { z } from "zod";
import { pool } from "../db.js";

/**
 * 用户表 MySQL 增删改查 Tool 集
 * 每个 Tool 都暴露给 qwen-plus,由 LLM 根据用户意图决定调用哪个。
 * 所有 SQL 均使用 ? 占位符参数化,防止 SQL 注入。
 */

/** 1. 查:分页查询所有用户 */
const listUsersTool = tool(
  async ({ page = 1, pageSize = 10 }) => {
    const offset = (page - 1) * pageSize;
    const [rows] = await pool.query(
      "SELECT * FROM users ORDER BY id DESC LIMIT ? OFFSET ?",
      [pageSize, offset]
    );
    const [[{ total }]] = await pool.query(
      "SELECT COUNT(*) AS total FROM users"
    );
    return JSON.stringify({ list: rows, total, page, pageSize });
  },
  {
    name: "list_users",
    description:
      "分页查询所有用户,返回用户列表和总数。可用于查看数据库里有哪些用户。",
    schema: z.object({
      page: z
        .number()
        .int()
        .min(1)
        .optional()
        .describe("页码,从 1 开始,默认 1"),
      pageSize: z
        .number()
        .int()
        .min(1)
        .max(100)
        .optional()
        .describe("每页条数,默认 10"),
    }),
  }
);

/** 2. 查:按 id 查询单个用户 */
const getUserTool = tool(
  async ({ id }) => {
    const [rows] = await pool.query("SELECT * FROM users WHERE id = ?", [id]);
    if (rows.length === 0) {
      return JSON.stringify({ message: `用户 id=${id} 不存在` });
    }
    return JSON.stringify(rows[0]);
  },
  {
    name: "get_user",
    description: "按 id 查询单个用户的详细信息。",
    schema: z.object({
      id: z.number().int().positive().describe("用户 id"),
    }),
  }
);

/** 3. 增:新增用户 */
const createUserTool = tool(
  async ({ name, email, age = 0 }) => {
    const [result] = await pool.query(
      "INSERT INTO users (name, email, age) VALUES (?, ?, ?)",
      [name, email, age]
    );
    return JSON.stringify({
      message: "新增成功",
      id: result.insertId,
      name,
      email,
      age,
    });
  },
  {
    name: "create_user",
    description:
      "新增一个用户,需要提供 name(姓名) 和 email(邮箱),age(年龄) 可选。邮箱重复时会报错。",
    schema: z.object({
      name: z.string().describe("用户姓名"),
      email: z.string().email().describe("用户邮箱,必须唯一"),
      age: z.number().int().min(0).optional().describe("用户年龄,默认 0"),
    }),
  }
);

/** 4. 改:按 id 修改用户 */
const updateUserTool = tool(
  async ({ id, name, email, age }) => {
    const [result] = await pool.query(
      "UPDATE users SET name = ?, email = ?, age = ? WHERE id = ?",
      [name, email, age ?? 0, id]
    );
    if (result.affectedRows === 0) {
      return JSON.stringify({ message: `用户 id=${id} 不存在,修改失败` });
    }
    return JSON.stringify({ message: "修改成功", id, name, email, age });
  },
  {
    name: "update_user",
    description: "按 id 修改用户的姓名、邮箱、年龄。",
    schema: z.object({
      id: z.number().int().positive().describe("要修改的用户 id"),
      name: z.string().describe("新的姓名"),
      email: z.string().email().describe("新的邮箱,必须唯一"),
      age: z.number().int().min(0).optional().describe("新的年龄"),
    }),
  }
);

/** 5. 删:按 id 删除用户 */
const deleteUserTool = tool(
  async ({ id }) => {
    const [result] = await pool.query("DELETE FROM users WHERE id = ?", [id]);
    if (result.affectedRows === 0) {
      return JSON.stringify({ message: `用户 id=${id} 不存在,删除失败` });
    }
    return JSON.stringify({ message: `删除成功,id=${id}` });
  },
  {
    name: "delete_user",
    description: "按 id 删除一个用户。",
    schema: z.object({
      id: z.number().int().positive().describe("要删除的用户 id"),
    }),
  }
);

export const userTools = [
  listUsersTool,
  getUserTool,
  createUserTool,
  updateUserTool,
  deleteUserTool,
];
