package quickrun

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParamEnvPrefix 是执行参数注入进程环境变量的统一前缀。
const ParamEnvPrefix = "SCRIPTBOARD_PARAM_"

// ParamVariablePrefix 是执行参数在启动参数模板中的变量前缀（{{PARAM_<大写名>}}）。
const ParamVariablePrefix = "PARAM_"

const (
	ParamTypeString  ParamType = "string"
	ParamTypeNumber  ParamType = "number"
	ParamTypeBoolean ParamType = "boolean"
	ParamTypeEnum    ParamType = "enum"
)

type ParamType string

// ParamDef 描述快捷执行的一个运行时可填参数。
type ParamDef struct {
	Name     string    `json:"name"`
	Label    string    `json:"label,omitempty"`
	Type     ParamType `json:"type"`
	Required bool      `json:"required,omitempty"`
	Default  string    `json:"default,omitempty"`
	Options  []string  `json:"options,omitempty"`
}

var paramNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

// ParamEnvName 返回参数对应的进程环境变量名（大写）。
func ParamEnvName(name string) string {
	return ParamEnvPrefix + strings.ToUpper(name)
}

// ParamVariableName 返回参数在启动参数模板中的变量名（大写，带 PARAM_ 前缀）。
func ParamVariableName(name string) string {
	return ParamVariablePrefix + strings.ToUpper(name)
}

// paramLabel 返回错误消息中使用的参数展示名。
func paramLabel(def ParamDef) string {
	if def.Label != "" {
		return def.Name + "（" + def.Label + "）"
	}
	return def.Name
}

// ParseParamDefs 解析并校验参数定义 JSON；空串表示无参数。所有问题一次性汇总返回。
func ParseParamDefs(jsonText string) ([]ParamDef, error) {
	if strings.TrimSpace(jsonText) == "" {
		return nil, nil
	}
	var defs []ParamDef
	if err := json.Unmarshal([]byte(jsonText), &defs); err != nil {
		return nil, fmt.Errorf("参数定义不是合法的 JSON：%v", err)
	}
	var problems []string
	if len(defs) > 20 {
		problems = append(problems, "参数最多 20 个")
	}
	seen := make(map[string]bool, len(defs))
	for index, def := range defs {
		where := fmt.Sprintf("第 %d 个参数", index+1)
		if !paramNamePattern.MatchString(def.Name) {
			problems = append(problems, where+"的名称无效（需以字母开头，仅含字母、数字、下划线，最长 32 字符）")
		} else {
			key := strings.ToUpper(def.Name)
			if seen[key] {
				problems = append(problems, where+"的名称 "+def.Name+" 重复")
			}
			seen[key] = true
		}
		if len(def.Label) > 60 {
			problems = append(problems, where+"的展示名最长 60 字符")
		}
		switch def.Type {
		case ParamTypeString:
			if len(def.Default) > 500 {
				problems = append(problems, where+"的默认值最长 500 字符")
			}
		case ParamTypeNumber:
			if def.Default != "" {
				if _, err := strconv.ParseFloat(def.Default, 64); err != nil {
					problems = append(problems, where+"的默认值不是数字")
				}
			}
		case ParamTypeBoolean:
			if def.Default != "" && !strings.EqualFold(def.Default, "true") && !strings.EqualFold(def.Default, "false") {
				problems = append(problems, where+"的默认值必须是 true 或 false")
			}
		case ParamTypeEnum:
			if len(def.Options) == 0 || len(def.Options) > 20 {
				problems = append(problems, where+"的选项需 1-20 个")
			}
			optionSeen := make(map[string]bool, len(def.Options))
			valid := true
			for _, option := range def.Options {
				if len(option) == 0 || len(option) > 60 {
					problems = append(problems, where+"的选项需 1-60 字符")
					valid = false
					continue
				}
				if optionSeen[option] {
					problems = append(problems, where+"的选项 "+option+" 重复")
					valid = false
				}
				optionSeen[option] = true
			}
			if def.Default != "" && valid && !optionSeen[def.Default] {
				problems = append(problems, where+"的默认值不在选项内")
			}
		default:
			problems = append(problems, where+"的类型无效（支持 string、number、boolean、enum）")
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("参数定义无效：%s", strings.Join(problems, "；"))
	}
	return defs, nil
}

// ResolveParamValues 按定义强类型校验取值并规范化，返回 参数名->值。
// values 为空时全部走默认值（非交互路径兜底）；未定义的 values 键忽略。
func ResolveParamValues(defs []ParamDef, values map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(defs))
	var problems []string
	for _, def := range defs {
		value, provided := values[def.Name]
		if !provided {
			value = def.Default
		}
		label := paramLabel(def)
		switch def.Type {
		case ParamTypeString:
			if def.Required && value == "" {
				problems = append(problems, "参数 "+label+" 必填")
				continue
			}
			if len(value) > 500 {
				problems = append(problems, "参数 "+label+" 最长 500 字符")
				continue
			}
		case ParamTypeNumber:
			if value == "" {
				if def.Required {
					problems = append(problems, "参数 "+label+" 必填")
				}
				continue
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				problems = append(problems, "参数 "+label+" 必须是数字")
				continue
			}
			value = strconv.FormatFloat(parsed, 'f', -1, 64)
		case ParamTypeBoolean:
			if value == "" {
				if def.Required {
					problems = append(problems, "参数 "+label+" 必填")
				}
				continue
			}
			if !strings.EqualFold(value, "true") && !strings.EqualFold(value, "false") {
				problems = append(problems, "参数 "+label+" 必须是 true 或 false")
				continue
			}
			value = strings.ToLower(value)
		case ParamTypeEnum:
			if value == "" {
				if def.Required {
					problems = append(problems, "参数 "+label+" 必填")
				}
				continue
			}
			matched := false
			for _, option := range def.Options {
				if value == option {
					matched = true
					break
				}
			}
			if !matched {
				problems = append(problems, "参数 "+label+" 不在可选值内")
				continue
			}
		}
		resolved[def.Name] = value
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "；"))
	}
	return resolved, nil
}

// ResolveParamEnv 按定义强类型校验取值并产出环境变量列表。
func ResolveParamEnv(defs []ParamDef, values map[string]string) ([]string, error) {
	resolved, err := ResolveParamValues(defs, values)
	if err != nil {
		return nil, err
	}
	return ParamEnvEntries(resolved), nil
}

// ParamEnvEntries 把已规范化的取值转换为环境变量条目。
func ParamEnvEntries(resolved map[string]string) []string {
	environment := make([]string, 0, len(resolved))
	for name, value := range resolved {
		environment = append(environment, ParamEnvName(name)+"="+value)
	}
	return environment
}

// ParamVariableEntries 把已规范化的取值转换为启动参数模板变量条目（PARAM_<大写名>）。
func ParamVariableEntries(resolved map[string]string) map[string]string {
	variables := make(map[string]string, len(resolved))
	for name, value := range resolved {
		variables[ParamVariableName(name)] = value
	}
	return variables
}

// ResolveParamVariables 按定义强类型校验取值并产出启动参数模板变量条目。
func ResolveParamVariables(defs []ParamDef, values map[string]string) (map[string]string, error) {
	resolved, err := ResolveParamValues(defs, values)
	if err != nil {
		return nil, err
	}
	return ParamVariableEntries(resolved), nil
}

// ParamValidationVariables 生成保存期模板校验用的占位变量（键存在即可，值取默认值）。
func ParamValidationVariables(defs []ParamDef) map[string]string {
	variables := make(map[string]string, len(defs))
	for _, def := range defs {
		variables[ParamVariableName(def.Name)] = def.Default
	}
	return variables
}
