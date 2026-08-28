//go:build mysqlintegration

package mysqlmanager

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

type dockerClientRunner struct {
	container, password, flavor string
}

func (runner dockerClientRunner) Run(ctx context.Context, executable string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	tool := executable
	if runner.flavor == "mariadb" {
		if executable == "mysqldump" {
			tool = "mariadb-dump"
		} else if executable == "mysql" {
			tool = "mariadb"
		}
	}
	if len(arguments) == 1 && arguments[0] == "--version" {
		command := exec.CommandContext(ctx, "docker", "exec", runner.container, tool, "--version")
		command.Stdout, command.Stderr = stdout, stderr
		return command.Run()
	}
	forwarded := []string{"exec", "-i", runner.container, tool, "-h127.0.0.1", "-P3306", "-uroot", "--password=" + runner.password}
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "--defaults-extra-file=") {
			forwarded = append(forwarded, argument)
		}
	}
	command := exec.CommandContext(ctx, "docker", forwarded...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	return command.Run()
}

func TestLogicalBackupAndRestoreAgainstContainer(t *testing.T) {
	container := os.Getenv("SCRIPTBOARD_MYSQL_INTEGRATION_CONTAINER")
	portText := os.Getenv("SCRIPTBOARD_MYSQL_INTEGRATION_PORT")
	password := os.Getenv("SCRIPTBOARD_MYSQL_INTEGRATION_PASSWORD")
	flavor := os.Getenv("SCRIPTBOARD_MYSQL_INTEGRATION_FLAVOR")
	if container == "" || portText == "" || password == "" {
		t.Skip("container, port, and password integration variables are required")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	serverDB, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/?multiStatements=true&parseTime=true", password, port))
	if err != nil {
		t.Fatal(err)
	}
	defer serverDB.Close()
	_, err = serverDB.ExecContext(ctx, `DROP DATABASE IF EXISTS sb_source; DROP DATABASE IF EXISTS sb_restored;
		CREATE DATABASE sb_source CHARACTER SET utf8mb4; USE sb_source;
		CREATE TABLE items(id INT PRIMARY KEY, name VARCHAR(64), touched INT NOT NULL DEFAULT 0);
		INSERT INTO items VALUES (1,'logical-backup',0);
		CREATE VIEW item_names AS SELECT name FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	objectDB, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/sb_source?parseTime=true", password, port))
	if err != nil {
		t.Fatal(err)
	}
	defer objectDB.Close()
	for _, statement := range []string{
		"CREATE TRIGGER items_touch BEFORE UPDATE ON items FOR EACH ROW SET NEW.touched=OLD.touched+1",
		"CREATE PROCEDURE item_count() SELECT COUNT(*) AS total FROM items",
		"CREATE FUNCTION item_constant() RETURNS INT DETERMINISTIC RETURN 1",
		"CREATE EVENT prune_nothing ON SCHEDULE EVERY 1 DAY DO DELETE FROM items WHERE id < 0",
	} {
		if _, err := objectDB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	stateRoot := t.TempDir()
	stateDB, err := sql.Open("sqlite", filepath.Join(stateRoot, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateDB.Close()
	applyTestSchema(t, stateDB)
	manager, err := New(Options{DB: stateDB, StateRoot: stateRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager.runner = dockerClientRunner{container: container, password: password, flavor: flavor}
	instance, err := manager.SaveInstance(ctx, InstanceInput{Name: flavor, Host: "127.0.0.1", Port: port, Username: "root", Password: password, TLSMode: TLSDisabled})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.Backup(ctx, BackupRequest{InstanceID: instance.ID, Database: "sb_source", Kind: BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	clearOperation, err := manager.BackupAndClearDatabase(ctx, BackupAndClearDatabaseRequest{
		InstanceID: instance.ID, Database: "sb_source", Confirmation: "sb_source", Actor: Actor{Username: "integration"},
	})
	if err != nil || clearOperation.Phase != "completed" || clearOperation.SafetyBackupID == "" {
		t.Fatalf("clear operation=%+v error=%v", clearOperation, err)
	}
	for objectType, query := range map[string]string{
		"database": "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='sb_source'",
		"table":    "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='sb_source'",
		"routine":  "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='sb_source'",
		"event":    "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA='sb_source'",
	} {
		var count int
		if err := serverDB.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("cleared %s query error=%v", objectType, err)
		}
		want := 0
		if objectType == "database" {
			want = 1
		}
		if count != want {
			t.Fatalf("cleared %s count=%d, want %d", objectType, count, want)
		}
	}
	operation, err := manager.Restore(ctx, RestoreRequest{InstanceID: instance.ID, BackupID: backup.ID, TargetDatabase: "sb_restored", Actor: Actor{Username: "integration"}})
	if err != nil || operation.Phase != "completed" {
		t.Fatalf("restore operation=%+v error=%v", operation, err)
	}
	var name string
	if err := serverDB.QueryRowContext(ctx, "SELECT name FROM sb_restored.item_names").Scan(&name); err != nil || name != "logical-backup" {
		t.Fatalf("restored view result=%q error=%v", name, err)
	}
	for objectType, query := range map[string]string{
		"trigger":   "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA='sb_restored'",
		"procedure": "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='sb_restored' AND ROUTINE_TYPE='PROCEDURE'",
		"function":  "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='sb_restored' AND ROUTINE_TYPE='FUNCTION'",
		"event":     "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA='sb_restored'",
	} {
		var count int
		if err := serverDB.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("restored %s count=%d error=%v", objectType, count, err)
		}
	}
}
