# ScriptBoard 代码结构重构执行计划

状态：已在 `codex/codebase-restructure` 执行

基线：`dev` `877b79f`

范围：最后正式版本之后的提交评审、信任边界修复、目录重组与发布门禁

## 交付原则

本次改动在同一功能分支一次性交付，但保留可独立验证和回滚的细粒度提交。`dev` 不接收迁移中间态；只有全量 Go、浏览器、race、fuzz 和发布合同门禁通过后才合并。

依赖方向固定为：

```text
cmd -> bootstrap -> web / domain -> store / leaf packages
```

禁止恢复 `internal/app`，禁止在 `cmd` 入口构造运行时领域依赖，禁止把 Broker-owned secret 放回 State Root。

## 执行切片

1. 修复 Registry 凭据边界：Broker 密封保存，Web/SQLite 通过 prepare/commit/abort 与持久操作日志收敛，迁移旧凭据。
2. 修复 External Interface 完成记录：持久 reconciliation 队列、幂等重放、过期状态转 `unknown`。
3. 建立 `internal/web`、规范化 `ui/` 资源根并删除 `internal/app`。
4. 建立 `internal/bootstrap`，集中 Web、Broker、Runner 和 AI Host 的进程组合；合并 Windows SCM 生命周期。
5. 建立 `internal/store/sqlite` 与 `internal/store/migrations`，把只读 header 预检、连接加固、完整性检查、事务迁移和 checkpoint 移出 Web。
6. 把授权规则、Quick Run 源码规则、文件操作持久化和审计保留分别收进 `identity`、`quickrun`、`fileworkflow` 与 `audit`。
7. 添加架构门禁：旧目录不可恢复、UI 根唯一、运行时入口必须委托 bootstrap、ADR 重复编号不可继续增加。
8. 明确 development installer 只验证 `--version-json`，不得安装或解包受管服务。
9. 同步 ADR、README、产品与数据模型说明，执行全部发布前门禁。

## 验收命令

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
Set-Location integration/browser
pnpm install --frozen-lockfile
pnpm test
```

```bash
bash ./scripts/run-race-security-gate.sh
bash ./scripts/run-fuzz-security-gate.sh
```

合并后仍须从 `dev` 创建不可变的 `release/X.Y.Z` 和同提交 `vX.Y.Z` Tag，并使用仓库现有 release workflow；本分支不直接发布。
