# 将 Pi Runtime 固定到签名的 ScriptBoard Release

生产主机不从 npm、PATH 或 Pi 上游的 `latest` 直接安装和更新 Assistant Runtime。每个 ScriptBoard Release 明确固定兼容的 Pi 版本、RPC 合同、平台资产摘要、许可证和来源元数据，并使用与应用更新密钥相同的信任根、不同的产品域生成独立 Runtime 清单与 detached signature；现有应用更新清单 Schema 保持不变。

Runtime Manager 只接受固定官方仓库或管理员上传的完整离线资产。下载或上传内容先进入 State Root 私有 staging 目录，再验证签名、仓库、ScriptBoard 兼容范围、平台、架构、大小、SHA-256、归档路径、链接、重复条目、许可证、唯一入口以及清单声明的伴随资源；不得只抽取 `pi` / `pi.exe`，因为独立包仍可能在启动时读取同版本目录中的主题或原生模块。通过 RPC 验活后才以同卷原子指针激活。任何失败都不能留下可被解析为活动版本的部分目录。

安装、切换和回退均由管理员明确发起，不静默进行。存在活动 Agent Turn、待处理审批或正在启动的 Pi 进程时拒绝切换；成功切换后旧对话在下一 Turn 按兼容 session 恢复。至少保留活动版本和一个已验证回退版本，清理不得删除被活动指针、回退指针或持久对话引用的内容。

用户自行安装或更新 Pi 不改变 ScriptBoard Runtime；ScriptBoard 更新 Runtime 也不修改用户 Pi。开发环境可以显式安装未签名 fixture，但必须标记为 development，不能被正式构建当作受信 Runtime。
