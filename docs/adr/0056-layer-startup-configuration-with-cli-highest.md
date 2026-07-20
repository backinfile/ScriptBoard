# 启动配置按默认值、YAML、环境变量和命令行分层

ScriptBoard 的配置优先级为内置默认值、YAML 配置文件、`SCRIPTBOARD_` 环境变量、命令行参数，后者覆盖前者；默认文件位于 Linux `/etc/scriptboard/config.yaml` 或 Windows `%ProgramData%\ScriptBoard\config.yaml`，也可用 `--config` 指定。命令行覆盖不写回文件，MVP 网页只读展示最终生效配置及非敏感来源。管理员凭据重设是会写入数据库的特殊启动操作，密码值绝不输出到日志。
