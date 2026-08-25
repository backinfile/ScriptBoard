package mysqlmanager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SQLMode string

const (
	SQLModeReadOnly SQLMode = "read_only"
	SQLModeWrite    SQLMode = "write"

	defaultSQLTimeout = 15 * time.Second
	maximumSQLTimeout = 2 * time.Minute
	defaultSQLRows    = 200
	maximumSQLRows    = 1000
)

type SQLRequest struct {
	Database, Statement string
	Mode                SQLMode
	Timeout             time.Duration
	MaxRows             int
	AllowDangerous      bool
	Actor               Actor
}

type SQLValue struct {
	Text string
	Null bool
}

type SQLResult struct {
	Columns                    []string
	Rows                       [][]SQLValue
	ReturnedRows, AffectedRows int64
	Duration                   time.Duration
	Truncated                  bool
	StatementType              string
}

type DatabaseObject struct {
	Database, Name, Type, Engine string
	Rows, SizeBytes              uint64
}

type ObjectColumn struct {
	Name, DataType, ColumnType, Extra, Comment string
	Nullable                                   bool
	Default                                    *string
	Ordinal                                    int
	PrimaryKey                                 bool
}

type ObjectIndex struct {
	Name, Type string
	Columns    []string
	Unique     bool
	Primary    bool
}

type ObjectDetails struct {
	Object  DatabaseObject
	Columns []ObjectColumn
	Indexes []ObjectIndex
}

// QueryBackend is intentionally separate from Backend so deployments can roll
// out browsing support without invalidating existing credential-only backends.
type QueryBackend interface {
	DatabasesIncludingSystem(context.Context, Instance) ([]Database, error)
	Objects(context.Context, Instance, string) ([]DatabaseObject, error)
	ObjectDetails(context.Context, Instance, string, string) (ObjectDetails, error)
	ExecuteSQL(context.Context, Instance, SQLRequest) (SQLResult, error)
}

func (m *Manager) queryBackend() (QueryBackend, error) {
	backend, ok := m.backend.(QueryBackend)
	if !ok {
		return nil, errors.New("MySQL query browsing is unavailable in this deployment")
	}
	return backend, nil
}

func (m *Manager) DatabasesIncludingSystem(ctx context.Context, id string) ([]Database, error) {
	instance, backend, err := m.queryInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	return backend.DatabasesIncludingSystem(ctx, instance)
}

func (m *Manager) Objects(ctx context.Context, id, database string) ([]DatabaseObject, error) {
	instance, backend, err := m.queryInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateDatabaseName(database); err != nil {
		return nil, err
	}
	return backend.Objects(ctx, instance, database)
}

func (m *Manager) ObjectDetails(ctx context.Context, id, database, object string) (ObjectDetails, error) {
	instance, backend, err := m.queryInstance(ctx, id)
	if err != nil {
		return ObjectDetails{}, err
	}
	if err := validateDatabaseName(database); err != nil {
		return ObjectDetails{}, err
	}
	if err := validateObjectName(object); err != nil {
		return ObjectDetails{}, err
	}
	return backend.ObjectDetails(ctx, instance, database, object)
}

func (m *Manager) PreviewRows(ctx context.Context, id, database, object string, limit int) (SQLResult, error) {
	if err := validateDatabaseName(database); err != nil {
		return SQLResult{}, err
	}
	if err := validateObjectName(object); err != nil {
		return SQLResult{}, err
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	return m.ExecuteSQL(ctx, id, SQLRequest{Database: database, Statement: "SELECT * FROM " + quoteIdentifier(object) + " LIMIT " + strconv.Itoa(limit), Mode: SQLModeReadOnly, MaxRows: limit})
}

func (m *Manager) ExecuteSQL(ctx context.Context, id string, request SQLRequest) (result SQLResult, err error) {
	instance, backend, err := m.queryInstance(ctx, id)
	if err != nil {
		return SQLResult{}, err
	}
	request.Database = strings.TrimSpace(request.Database)
	request.Statement = strings.TrimSpace(request.Statement)
	if err := validateDatabaseName(request.Database); err != nil {
		return SQLResult{}, err
	}
	if request.Mode == "" {
		request.Mode = SQLModeReadOnly
	}
	if request.Mode != SQLModeReadOnly && request.Mode != SQLModeWrite {
		return SQLResult{}, errors.New("invalid MySQL SQL mode")
	}
	request.Timeout, request.MaxRows = boundedSQLLimits(request.Timeout, request.MaxRows)
	started := m.now()
	result, err = backend.ExecuteSQL(ctx, instance, request)
	result.Duration = m.now().Sub(started)
	resultText := "succeeded"
	if err != nil {
		resultText = "failed"
	}
	digest := sha256.Sum256([]byte(request.Statement))
	target := fmt.Sprintf("instance=%s database=%s type=%s duration_ms=%d affected=%d returned=%d sql_sha256=%s", instance.ID, request.Database, result.StatementType, result.Duration.Milliseconds(), result.AffectedRows, result.ReturnedRows, hex.EncodeToString(digest[:]))
	m.recordAudit(AuditEvent{Action: "execute_mysql_sql", Target: target, Result: resultText, Actor: request.Actor})
	return result, err
}

func (m *Manager) queryInstance(ctx context.Context, id string) (Instance, QueryBackend, error) {
	instance, err := m.Instance(ctx, id)
	if err != nil {
		return Instance{}, nil, err
	}
	backend, err := m.queryBackend()
	return instance, backend, err
}

func boundedSQLLimits(timeout time.Duration, rows int) (time.Duration, int) {
	if timeout <= 0 {
		timeout = defaultSQLTimeout
	}
	if timeout > maximumSQLTimeout {
		timeout = maximumSQLTimeout
	}
	if rows <= 0 {
		rows = defaultSQLRows
	}
	if rows > maximumSQLRows {
		rows = maximumSQLRows
	}
	return timeout, rows
}

func validateDatabaseName(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 64 || strings.ContainsRune(value, 0) {
		return errors.New("invalid MySQL database name")
	}
	return nil
}

func validateObjectName(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 64 || strings.ContainsRune(value, 0) {
		return errors.New("invalid MySQL object name")
	}
	return nil
}

type sqlClassification struct {
	kind, normalized string
	readOnly, query  bool
	dangerous        bool
}

func classifySQL(statement string) (sqlClassification, error) {
	normalized, words, err := scanSQL(statement)
	if err != nil {
		return sqlClassification{}, err
	}
	if len(words) == 0 {
		return sqlClassification{}, errors.New("SQL statement is required")
	}
	kind := words[0].word
	if kind == "WITH" {
		for _, token := range words[1:] {
			if token.depth > 0 && token.word != "SELECT" && isStatementKeyword(token.word) {
				return sqlClassification{}, errors.New("data-changing statements are not allowed inside a CTE")
			}
		}
		kind = ""
		for _, token := range words[1:] {
			if token.depth == 0 && isStatementKeyword(token.word) {
				kind = token.word
			}
		}
		if kind == "" {
			return sqlClassification{}, errors.New("WITH statement has no supported terminal query")
		}
	}
	classification := sqlClassification{kind: kind, normalized: normalized}
	switch kind {
	case "SELECT":
		classification.readOnly, classification.query = true, true
		if containsTopLevelSequence(words, "INTO", "OUTFILE") || containsTopLevelSequence(words, "INTO", "DUMPFILE") || containsTopLevelSequence(words, "FOR", "UPDATE") || containsTopLevelSequence(words, "LOCK", "IN", "SHARE", "MODE") {
			return sqlClassification{}, errors.New("locking or server-file SELECT is not allowed")
		}
	case "SHOW", "DESC", "DESCRIBE", "EXPLAIN":
		classification.readOnly, classification.query = true, true
	case "INSERT", "REPLACE", "CREATE", "RENAME":
		classification.dangerous = kind == "CREATE" || kind == "RENAME"
	case "UPDATE", "DELETE":
		classification.dangerous = !containsTopLevelWord(words, "WHERE")
	case "DROP", "TRUNCATE", "ALTER":
		classification.dangerous = true
	default:
		return sqlClassification{}, fmt.Errorf("unsupported SQL statement type %s", kind)
	}
	return classification, nil
}

type sqlWord struct {
	word  string
	depth int
}

func scanSQL(statement string) (string, []sqlWord, error) {
	statement = strings.TrimSpace(statement)
	var normalized strings.Builder
	var words []sqlWord
	depth, semicolons := 0, 0
	for index := 0; index < len(statement); {
		character := statement[index]
		if character == '\'' || character == '"' || character == '`' {
			quote := character
			normalized.WriteByte(character)
			index++
			closed := false
			for index < len(statement) {
				current := statement[index]
				normalized.WriteByte(current)
				index++
				if current == '\\' && index < len(statement) {
					normalized.WriteByte(statement[index])
					index++
					continue
				}
				if current == quote {
					if index < len(statement) && statement[index] == quote {
						normalized.WriteByte(statement[index])
						index++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return "", nil, errors.New("unterminated SQL quoted value")
			}
			continue
		}
		if character == '#' || (character == '-' && index+1 < len(statement) && statement[index+1] == '-' && (index+2 == len(statement) || statement[index+2] <= ' ')) {
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
			normalized.WriteByte(' ')
			continue
		}
		if character == '/' && index+1 < len(statement) && statement[index+1] == '*' {
			end := strings.Index(statement[index+2:], "*/")
			if end < 0 {
				return "", nil, errors.New("unterminated SQL comment")
			}
			index += end + 4
			normalized.WriteByte(' ')
			continue
		}
		if character == ';' {
			semicolons++
			if strings.TrimSpace(statement[index+1:]) != "" {
				return "", nil, errors.New("multiple SQL statements are not allowed")
			}
			index++
			continue
		}
		if character == '(' {
			depth++
			normalized.WriteByte(character)
			index++
			continue
		}
		if character == ')' {
			depth--
			if depth < 0 {
				return "", nil, errors.New("unbalanced SQL parentheses")
			}
			normalized.WriteByte(character)
			index++
			continue
		}
		if isSQLWordByte(character) {
			start := index
			for index < len(statement) && isSQLWordByte(statement[index]) {
				index++
			}
			word := strings.ToUpper(statement[start:index])
			words = append(words, sqlWord{word: word, depth: depth})
			normalized.WriteString(statement[start:index])
			continue
		}
		normalized.WriteByte(character)
		index++
	}
	if depth != 0 {
		return "", nil, errors.New("unbalanced SQL parentheses")
	}
	if semicolons > 1 {
		return "", nil, errors.New("multiple SQL statements are not allowed")
	}
	return strings.TrimSpace(normalized.String()), words, nil
}

func isSQLWordByte(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func isStatementKeyword(value string) bool {
	switch value {
	case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN", "INSERT", "REPLACE", "UPDATE", "DELETE", "CREATE", "DROP", "TRUNCATE", "ALTER", "RENAME":
		return true
	default:
		return false
	}
}

func containsTopLevelWord(words []sqlWord, value string) bool {
	for _, word := range words {
		if word.depth == 0 && word.word == value {
			return true
		}
	}
	return false
}

func containsTopLevelSequence(words []sqlWord, values ...string) bool {
	position := 0
	for _, word := range words {
		if word.depth != 0 {
			continue
		}
		if word.word == values[position] {
			position++
			if position == len(values) {
				return true
			}
		} else if word.word == values[0] {
			position = 1
		} else {
			position = 0
		}
	}
	return false
}

func executeRows(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, statement string, maximum int) (SQLResult, error) {
	rows, err := queryer.QueryContext(ctx, statement)
	if err != nil {
		return SQLResult{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return SQLResult{}, err
	}
	result := SQLResult{Columns: columns}
	const maximumResultBytes = 4 << 20
	const maximumCellBytes = 64 << 10
	resultBytes := 0
	for rows.Next() {
		if len(result.Rows) == maximum || resultBytes >= maximumResultBytes {
			result.Truncated = true
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return SQLResult{}, err
		}
		item := make([]SQLValue, len(columns))
		for index, value := range values {
			if value == nil {
				item[index].Null = true
				continue
			}
			switch typed := value.(type) {
			case []byte:
				item[index].Text = string(typed)
			default:
				item[index].Text = fmt.Sprint(typed)
			}
			if len(item[index].Text) > maximumCellBytes {
				item[index].Text = item[index].Text[:maximumCellBytes]
				result.Truncated = true
			}
			resultBytes += len(item[index].Text)
		}
		result.Rows = append(result.Rows, item)
	}
	result.ReturnedRows = int64(len(result.Rows))
	return result, rows.Err()
}
