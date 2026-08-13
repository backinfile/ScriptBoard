# 使用签名发布清单建立更新信任

Git Tag 是正式版本的唯一事实来源。Release 流水线为 Windows/Linux、amd64/arm64 四个平台各生成一个自解包安装器、载荷内 `RELEASE.json`、`SHA256SUMS`、严格 JSON 更新清单和 Ed25519 detached signature；正式 Tag 缺少签名密钥时必须失败，不能降级为未签名发布。

运行中的服务只接受固定仓库 `backinfile/ScriptBoard` 的发布信息与文件。传输可以直连 GitHub，或使用程序内置的固定公开代理；代理前仍先验证原始 GitHub URL，不能由管理员输入任意代理地址。系统只选择非 Draft、非 Prerelease 的稳定版本，并只信任编译进当前正式构建的 key ID 与公钥。清单绑定仓库、版本、Tag、Commit、数据库 Schema、updater 协议以及每个平台安装器的名称、大小、载荷解压大小和 SHA-256。自动更新直接下载与首次安装相同的 Setup EXE/`.run`，验证整个文件摘要后按 ZIP 载荷执行原有安全解包；未知字段、重复字段、协议过新、平台不匹配、摘要错误或不安全载荷均拒绝。

不增加由机器人回写 `VERSION` 文件的提交。签名密钥轮换通过桥接 Release 完成：先用旧密钥发布一个内置新公钥的正式版本，再从后续 Tag 改用新私钥；私钥只保存在 GitHub `release` Environment Secret 中。

Setup EXE/`.run` 资产把 updater protocol 提升为 2，发布清单的 `minimum_updater_protocol` 同步为 2。既有 protocol 1 客户端无法通过旧资产校验，因此必须由发布说明明确要求管理员手工安装新基线；完成一次手工安装后，自动更新继续直接消费同一种单文件安装器。
