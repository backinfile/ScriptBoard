# 将受管 Host Files 宿主访问限制在 Broker 内

受管 Web 不再直接列举、规范化、读取、写入、移动、删除或跟随宿主文件。页面、Assistant 引用、文本预览、图片与下载统一调用 Broker 的固定 Host Files 协议；上传和文本保存先写入 State Root 内的专用交换目录，Broker 以普通文件身份复核后消费暂存内容并发布到宿主路径。Broker 继续保护 State Root、实例主密钥目录、Broker 安装目录和 MySQL 备份根，因而普通 Host Files 能力不能转读这些私有领域。

协议按 Roots、List、Info、ReadText、Canonical、AvailableName、Mkdir、ToggleExecute、Trash、Restore、Purge、Move、OpenRead、ReadChunk、CloseRead、Upload、SaveText、Rollback、Remove、Prepare、SameFilesystem、Append 与 Log 固定操作拆分，并对每种操作执行完整 payload 等值检查，拒绝通用参数和其他凭据领域字段。读操作要求有效 Administrator、Maintainer 或 Operator 会话；写操作要求 Administrator/Maintainer；删除、移动和执行位变更继续要求近期认证。下载和日志句柄使用 192-bit 随机值，绑定授权用户、短时续租且有容量上限，避免跨用户消费和无限句柄泄漏。

跨文件系统移动的扫描、复制、摘要验证、目标提交、源回收、崩溃恢复和数据库引用更新由 Broker 持有的 MoveEngine 完成；Web 只创建固定请求并读取共享数据库中的进度。Broker 启动时先恢复未完成操作。相同文件系统的原子移动也由 Broker 执行。实时文件日志在 Broker 内打开并保持源版本与边界摘要，Web 只通过有界历史页和两秒短轮询转发 SSE。

没有网页登录会话的调用不得借用或伪造可重放会话。External Interface 日志写入使用单独的固定操作，Broker 重新解析一次性呈现的 Trigger Key、Entry ID/名称、动作类型和已提交配置后才追加；Scheduler 只能按数据库中未删除的 Schedule ID 请求 Broker 准备已配置脚本；Runner 在执行点再次验证脚本摘要和工作目录。Assistant Tool Broker 绑定发起浏览器的特权会话，配置身份包含会话摘要，会话变化会撤销旧 Runtime 能力。

MySQL 导入宿主文件通过 Broker Host Files 分块读取，MySQL 备份下载使用独立的 Broker MySQL 分块操作；后者按备份 ID 重新查询共享数据库，限制在配置备份根内并复核普通文件身份和记录大小。Web 不再直接打开 MySQL 备份根。

独立 Web/Broker 拓扑测试覆盖 Broker 下线时的 fail-closed 列表访问、大文件上传下载、交换目录清理、固定协议字段拒绝、受保护路径和用户绑定句柄。完整 Go 测试、vet、Windows 构建与 Linux `CGO_ENABLED=0` 构建作为本切片门禁。Windows SCM 身份/ACL 与真实 Linux systemd 安装仍属于发布平台门禁，不能由进程内集成测试替代。
