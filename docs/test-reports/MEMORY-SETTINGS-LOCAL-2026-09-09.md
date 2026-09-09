# 全局内存设置本地测试（2026-09-09）

## 测试范围

- 登录与监控、快捷任务、计划任务、运行历史、设置页基础访问。
- 总额度、默认任务额度与平台专属额度保存，非法值、陈旧表单、CSRF、跨域及角色权限。
- 配置注释和其他字段保留、并发保存、Windows ACL、Linux 文件权限、Broker 授权与固定路径。
- 保存后旧进程配置不变，重启后加载新值；中英文、桌面和 390px 移动布局。

## 结果

- Windows `go test ./... -count=1` 通过；最终相关模块 `MemorySettings|FixedRoles` 测试通过，包括过期身份确认不写入配置。
- `go vet ./...` 通过。
- Linux `go test -race ./internal/config ./internal/privilegebroker ./internal/web -run 'MemorySettings|FixedRoles' -count=1` 通过；平台服务内存策略测试通过。
- 外部 Chromium 实例验证登录、基础访问、界面保存、错误处理、重启读取、中英文与移动布局通过。
- 使用 Windows 便携实例完成实际界面与重启验证；受管服务写入路径由 Broker 测试覆盖，未实际安装或重启系统服务。

## 保留的测试部署

- 地址：http://127.0.0.1:6427/settings/memory
- 用户：`admin`，密码：`Memory-Settings-Test-2026!`
- 当前配置：总额度 `768MiB`、默认任务 `192MiB`、单进程 `256MiB`。
- 部署、状态和截图保留于 `D:\Github\worktrees\ScriptBoard\memory-settings\.scratch\memory-settings-local`。
- `browser-results.json` 保存检查结果，`desktop.png` 与 `mobile.png` 保存截图；完整测试日志位于同一 worktree 的 `.scratch`。
- 保存配置后需要重启服务；环境变量和命令行参数仍优先。为保留测试部署，此次保留任务 worktree。

完整浏览器门禁 `npm test --prefix integration/browser` 最终通过。更新了设置导航的预期入口；文件跳转用例首次超时，未改动该功能，复跑通过。
