# MySQL 恢复禁用本地客户端命令并限制 gzip 展开

所有导入和恢复均通过同一个固定参数构造器启动 `mysql`/`mariadb` 客户端。参数强制包含
`--binary-mode --batch --skip-reconnect`，连接字符集固定为 `utf8mb4`，目标数据库放在
`--` 之后。不得根据上传内容、文件名或请求字段增加客户端 option。

MySQL 和 MariaDB 的官方客户端文档都说明：非交互 `--binary-mode` 会禁用除 charset 与
delimiter 外的客户端命令。这阻止导入 SQL 使用 `system`/`\!`、`source`、pager 或 tee
在 ScriptBoard 服务身份下访问宿主进程或文件，同时仍兼容 mysqldump 产生的 delimiter。

`.sql.gz` 在接收时以及每次恢复前都必须完整解码验证，非空解压内容上限为 8 GiB；超过
上限、CRC/流损坏或空内容均在启动数据库替换前 fail closed。压缩文件自身仍受 2 GiB
上传上限和 SHA-256 完整性保护。

参考：[MySQL 8.4 Client Commands](https://dev.mysql.com/doc/refman/8.4/en/mysql-commands.html)、
[MariaDB Command-Line Client](https://mariadb.com/docs/server/clients-and-utilities/mariadb-client/mariadb-command-line-client)。
