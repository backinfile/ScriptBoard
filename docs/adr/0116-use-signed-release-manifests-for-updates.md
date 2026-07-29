# 使用签名发布清单建立更新信任

Git Tag 是正式版本的唯一事实来源。Release 流水线为 Windows/Linux、amd64/arm64 四个平台生成完整归档、归档内 `RELEASE.json`、`SHA256SUMS`、严格 JSON 更新清单和 Ed25519 detached signature；正式 Tag 缺少签名密钥时必须失败，不能降级为未签名发布。

运行中的服务只访问固定仓库 `backinfile/ScriptBoard`，只选择非 Draft、非 Prerelease 的稳定版本，并只信任编译进当前正式构建的 key ID 与公钥。清单绑定仓库、版本、Tag、Commit、数据库 Schema、updater 协议以及每个平台归档的名称、大小、解压大小和 SHA-256。未知字段、重复字段、协议过新、平台不匹配、摘要错误或不安全归档均拒绝。

不增加由机器人回写 `VERSION` 文件的提交。签名密钥轮换通过桥接 Release 完成：先用旧密钥发布一个内置新公钥的正式版本，再从后续 Tag 改用新私钥；私钥只保存在 GitHub `release` Environment Secret 中。
