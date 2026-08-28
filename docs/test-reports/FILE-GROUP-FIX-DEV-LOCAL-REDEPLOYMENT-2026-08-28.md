# 文件分组修复合并后本地重部署报告

测试时间：2026-08-28（Asia/Shanghai）  
分支：`dev`  
提交：`278dcce`

## 结论

- 已从合并后的 `dev` 重新构建并启动本地实例。
- 重部署沿用原 State Root，丰富测试数据和登录凭据均保留。
- 登录页、管理员登录、监控页、文件页及文件快捷访问数据接口均验证通过。
- 服务仅监听回环地址，最终错误日志为空。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18869` |
| 进程 | `scriptboard-dev-278dcce.exe`，PID `37148` |
| 监听边界 | 仅 `127.0.0.1:18869` |
| State Root | `.scratch/local-deploy-richdata-final-20260828/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告 |
| 二进制 SHA-256 | `5E6EBCC7B6F2932AAF99A92E50007BB206C178C84591983EC1DD34DF04A52EF5` |

## 测试条目与结果

1. 从当前 `dev` 构建 `cmd/scriptboard`：通过。
2. 使用原 State Root 重启并监听 `127.0.0.1:18869`：通过。
3. 未认证访问 `/login`：HTTP 200。
4. 使用保留的管理员凭据登录：通过，进入 `/monitor`。
5. 认证访问 `/monitor`：HTTP 200。
6. 认证访问 `/resources/files`：HTTP 200。
7. 读取 `/resources/files/quick-access`：通过，保留 2 个快捷项和 5 个共享分组。
8. 检查最终标准错误日志：0 字节。

部署与测试数据在验证结束后继续保留。
