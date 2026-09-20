package quickrun

import (
	"slices"
	"strings"
	"testing"
)

func TestParseParamDefsEmptyMeansNoParams(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "  ", "null", "[]"} {
		defs, err := ParseParamDefs(text)
		if err != nil || len(defs) != 0 {
			t.Fatalf("ParseParamDefs(%q) = %v, %v", text, defs, err)
		}
	}
}

func TestParseParamDefsValidDefinition(t *testing.T) {
	t.Parallel()
	defs, err := ParseParamDefs(`[
		{"name":"Region","label":"区域","type":"enum","required":true,"default":"cn","options":["cn","us"]},
		{"name":"count","type":"number","default":"01.50"},
		{"name":"force","type":"boolean","default":"TRUE"},
		{"name":"note","type":"string"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 4 || defs[0].Name != "Region" || defs[0].Label != "区域" || !defs[0].Required {
		t.Fatalf("unexpected defs: %#v", defs)
	}
}

func TestParseParamDefsValidationMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		json    string
		problem string
	}{
		{"非法JSON", `[{"name":`, "JSON"},
		{"名称空", `[{"name":"","type":"string"}]`, "名称无效"},
		{"名称数字开头", `[{"name":"1abc","type":"string"}]`, "名称无效"},
		{"名称含横线", `[{"name":"a-b","type":"string"}]`, "名称无效"},
		{"名称超长", `[{"name":"` + strings.Repeat("a", 33) + `","type":"string"}]`, "名称无效"},
		{"名称重复大小写", `[{"name":"env","type":"string"},{"name":"ENV","type":"string"}]`, "重复"},
		{"展示名超长", `[{"name":"a","label":"` + strings.Repeat("展", 61) + `","type":"string"}]`, "展示名"},
		{"类型未知", `[{"name":"a","type":"date"}]`, "类型无效"},
		{"enum无选项", `[{"name":"a","type":"enum"}]`, "选项"},
		{"enum选项超长", `[{"name":"a","type":"enum","options":["` + strings.Repeat("x", 61) + `"]}]`, "选项"},
		{"enum选项重复", `[{"name":"a","type":"enum","options":["x","x"]}]`, "重复"},
		{"enum默认值越界", `[{"name":"a","type":"enum","options":["x"],"default":"y"}]`, "默认值不在选项内"},
		{"number默认值非法", `[{"name":"a","type":"number","default":"abc"}]`, "默认值不是数字"},
		{"boolean默认值非法", `[{"name":"a","type":"boolean","default":"yes"}]`, "true 或 false"},
		{"string默认值超长", `[{"name":"a","type":"string","default":"` + strings.Repeat("x", 501) + `"}]`, "默认值最长 500"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseParamDefs(testCase.json)
			if err == nil || !strings.Contains(err.Error(), testCase.problem) {
				t.Fatalf("err=%v, want substring %q", err, testCase.problem)
			}
		})
	}
}

func TestParseParamDefsRejectsTooManyParams(t *testing.T) {
	t.Parallel()
	var builder strings.Builder
	builder.WriteString("[")
	for index := 0; index < 21; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"name":"p` + strings.Repeat("a", index+1) + `","type":"string"}`)
	}
	builder.WriteString("]")
	if _, err := ParseParamDefs(builder.String()); err == nil || !strings.Contains(err.Error(), "最多 20 个") {
		t.Fatalf("err=%v, want 参数总数限制", err)
	}
}

func TestParseParamDefsAggregatesAllProblems(t *testing.T) {
	t.Parallel()
	_, err := ParseParamDefs(`[{"name":"1bad","type":"date"},{"name":"","type":"string"}]`)
	if err == nil {
		t.Fatal("want error")
	}
	for _, problem := range []string{"第 1 个参数", "第 2 个参数"} {
		if !strings.Contains(err.Error(), problem) {
			t.Fatalf("err=%q, want %q", err.Error(), problem)
		}
	}
}

func paramsFixture(t *testing.T) []ParamDef {
	t.Helper()
	defs, err := ParseParamDefs(`[
		{"name":"region","label":"区域","type":"enum","required":true,"default":"cn","options":["cn","us"]},
		{"name":"count","type":"number","default":"01.50"},
		{"name":"force","type":"boolean","default":"TRUE"},
		{"name":"note","type":"string"},
		{"name":"must","type":"string","required":true}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

func TestResolveParamEnvDefaultsFallback(t *testing.T) {
	t.Parallel()
	defs := paramsFixture(t)
	environment, err := ResolveParamEnv(defs, nil)
	if err == nil || !strings.Contains(err.Error(), "must") {
		t.Fatalf("required without default must fail: %v", err)
	}
	environment, err = ResolveParamEnv(defs[:4], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"SCRIPTBOARD_PARAM_REGION=cn",
		"SCRIPTBOARD_PARAM_COUNT=1.5",
		"SCRIPTBOARD_PARAM_FORCE=true",
		"SCRIPTBOARD_PARAM_NOTE=",
	} {
		if !slices.Contains(environment, required) {
			t.Fatalf("environment missing %q: %#v", required, environment)
		}
	}
}

func TestResolveParamEnvValidationMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		values  map[string]string
		problem string
	}{
		{"number拒绝abc", map[string]string{"count": "abc", "must": "x"}, "必须是数字"},
		{"boolean拒绝yes", map[string]string{"force": "yes", "must": "x"}, "true 或 false"},
		{"enum拒绝越界", map[string]string{"region": "eu", "must": "x"}, "不在可选值内"},
		{"必填缺失", map[string]string{}, "必填"},
		{"必填空串", map[string]string{"must": ""}, "必填"},
		{"string超长", map[string]string{"must": "x", "note": strings.Repeat("x", 501)}, "最长 500 字符"},
	}
	defs := paramsFixture(t)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveParamEnv(defs, testCase.values)
			if err == nil || !strings.Contains(err.Error(), testCase.problem) {
				t.Fatalf("err=%v, want substring %q", err, testCase.problem)
			}
			if testCase.problem == "必填" && !strings.Contains(err.Error(), "must") {
				t.Fatalf("err=%q, want 参数名 must", err.Error())
			}
		})
	}
}

func TestResolveParamEnvCoercesAndNormalizes(t *testing.T) {
	t.Parallel()
	defs := paramsFixture(t)
	environment, err := ResolveParamEnv(defs[:4], map[string]string{
		"region":  "us",
		"count":   "1e3",
		"force":   "False",
		"unknown": "忽略未定义的键",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"SCRIPTBOARD_PARAM_REGION=us",
		"SCRIPTBOARD_PARAM_COUNT=1000",
		"SCRIPTBOARD_PARAM_FORCE=false",
	} {
		if !slices.Contains(environment, required) {
			t.Fatalf("environment missing %q: %#v", required, environment)
		}
	}
	for _, entry := range environment {
		if strings.Contains(entry, "UNKNOWN") {
			t.Fatalf("undefined value leaked into environment: %#v", environment)
		}
	}
}

func TestResolveParamEnvEmptyProvidedValueIsValidated(t *testing.T) {
	t.Parallel()
	defs := paramsFixture(t)
	// 显式提供的空串也要按类型校验，不能静默落到默认值。
	if _, err := ResolveParamEnv(defs[:4], map[string]string{"count": ""}); err != nil {
		t.Fatalf("optional number with explicit empty value: %v", err)
	}
	if _, err := ResolveParamEnv(defs[:4], map[string]string{"region": ""}); err == nil {
		t.Fatal("required enum with explicit empty value must fail")
	}
}

func TestParamEnvNameUppercases(t *testing.T) {
	t.Parallel()
	if got := ParamEnvName("region_Id2"); got != "SCRIPTBOARD_PARAM_REGION_ID2" {
		t.Fatalf("ParamEnvName = %q", got)
	}
}

// 启动参数模板变量：{{PARAM_<大写名>}} 的产出与保存期占位。
func TestResolveParamVariablesAndEntries(t *testing.T) {
	t.Parallel()
	defs := paramsFixture(t)
	resolved, err := ResolveParamValues(defs, map[string]string{"region": "us", "must": "x"})
	if err != nil {
		t.Fatal(err)
	}
	variables := ParamVariableEntries(resolved)
	if variables["PARAM_REGION"] != "us" {
		t.Fatalf("PARAM_REGION=%q", variables["PARAM_REGION"])
	}
	if _, ok := variables["region"]; ok {
		t.Fatal("variable entries must use the PARAM_ prefix")
	}
	// 校验失败时不产出任何变量。
	if _, err := ResolveParamVariables(defs, map[string]string{"region": "xx", "must": "x"}); err == nil {
		t.Fatal("invalid enum value must fail variable resolution")
	}
	// 保存期占位：键存在、值取默认值。
	placeholders := ParamValidationVariables(defs)
	if placeholders["PARAM_REGION"] != defs[0].Default {
		t.Fatalf("placeholder=%q want default %q", placeholders["PARAM_REGION"], defs[0].Default)
	}
	if len(placeholders) != len(defs) {
		t.Fatalf("placeholders=%d want %d", len(placeholders), len(defs))
	}
	if got := ParamVariableName("region_Id2"); got != "PARAM_REGION_ID2" {
		t.Fatalf("ParamVariableName = %q", got)
	}
}
