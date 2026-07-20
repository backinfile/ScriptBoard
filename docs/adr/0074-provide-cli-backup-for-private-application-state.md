---
status: superseded by ADR-0100
---

# 提供应用私有状态的 CLI 备份与恢复

`scriptboard backup create` 使用 SQLite 在线备份机制生成一致快照，默认打包数据库和恢复所需的应用密钥，可选包含 Run 日志，但不包含服务日志、YAML 配置或受管根目录；受管文件由系统备份工具另行保护。`backup restore` 要求服务停止且格式版本兼容，覆盖前先保存当前状态，并在恢复后撤销全部 Session，避免恢复出的会话继续有效。
