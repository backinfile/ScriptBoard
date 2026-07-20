# 围绕活动执行批次创建 Git 检查点

版本保护启用后，无活动 Run 时网页文件变更完成即自动提交；首个 Run 启动前创建 `pre-run checkpoint`，任一 Run 活动期间暂停 Git add/commit，最后一个 Run 结束后创建列出该批 Run ID 的 `post-run checkpoint`，期间网页变更一并进入结果提交。异常退出后保留最近基线与脏工作树，重启不自动提交而要求人工查看、恢复或创建恢复提交。Git 历史是全局文件时间线，不宣称能把并发修改精确归因到单个 Run。
