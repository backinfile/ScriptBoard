# 按 Runtime 会话代理 Assistant Provider 凭据

受管 Pi Runtime 不再直接获得 Provider Endpoint 或 API Key。ScriptBoard 为每个 Pi
进程创建只监听 `127.0.0.1` 的临时 Provider 代理，向 Pi 的 `models.json` 写入该环回地址，
并只通过精确环境传入随机 256-bit capability。代理在服务进程内保存真实上游配置，随
Pi 进程停止、空闲回收、配置切换或服务关闭而撤销。

代理只接受当前 Provider 对应的单一 POST 推理路径，并要求请求 JSON 中的模型与会话
绑定模型完全一致。它限制请求和响应大小，只转发协议所需头，重新注入真实认证头，使用
共享 OutboundPolicy 固定 DNS 校验结果，不读取环境代理且拒绝所有重定向。Pi 启动器只
接受环回 HTTP Provider 地址，因此调用方不能退回直连上游。

此决策减少凭据在子进程参数、环境和配置文件中的暴露，也把正常 Provider 流量收口到
可检查边界；它不构成完整 OS 沙箱。当前 Runtime 仍与服务共享宿主身份，并可能自行建立
其他网络连接。P0-08 只有在独立身份/受限 Token、秘密目录 ACL、网络默认拒绝和资源限制
完成平台验证后才能标记完成。

ADR-0159 进一步把持有真实 Provider 凭据和上游网络能力的临时代理从受管 Web 迁入 Broker。Pi 侧环回地址、精确模型 capability 与随进程撤销的语义保持不变；受管 Web 现在只转交 Broker 返回的代理能力，不再读取 API Key。
