# 持久恢复 External Interface 完成记录且不重复动作

External Interface 动作可能已经产生不可逆副作用，而最终 invocation 更新可能因 SQLite 短暂故障或请求取消失败。此时不能向客户端伪报动作失败，否则自动重试可能重复运行脚本、写变量或接收文件。

动作开始前仍必须先提交 `processing` 记录；开始记录失败时不得执行动作。动作执行后若完成更新失败，Web 返回真实业务结果和原 Request ID，同时把完整、脱敏且有界的完成结果原子写入 State Root 操作队列。后台与启动流程按 invocation ID 幂等回放，成功后删除队列文件。队列写入失败必须可观测，不能静默忽略。

没有完成结果且超过恢复期限的 `processing` 记录转为 `unknown`，明确表示动作可能已执行；不得推断为 `failed`，也不得自动重新执行动作。该状态保留取证事实，并与记录了 completion deferred 的审计事件关联。
