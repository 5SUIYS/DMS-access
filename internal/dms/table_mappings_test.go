package dms_test

// Feature: mysql-redshift-migration-approval, Property 7: table-mappings 生成正确性

import (
	"encoding/json"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5miles/dms-access/internal/dms"
	"github.com/5miles/dms-access/internal/domain"
)

// TestBuildTableMappings_Basic 验证基本功能。
func TestBuildTableMappings_Basic(t *testing.T) {
	selections := []domain.TableSelection{
		{SchemaName: "mydb", TableName: "users"},
		{SchemaName: "mydb", TableName: "orders"},
	}

	result := dms.BuildTableMappings(selections)
	assert.NotEmpty(t, result)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &out))

	rules := out["rules"].([]interface{})
	assert.Equal(t, 2, len(rules), "规则数量应与输入相同")

	for i, sel := range selections {
		rule := rules[i].(map[string]interface{})
		assert.Equal(t, "selection", rule["rule-type"])
		assert.Equal(t, "include", rule["rule-action"])

		loc := rule["object-locator"].(map[string]interface{})
		assert.Equal(t, sel.SchemaName, loc["schema-name"])
		assert.Equal(t, sel.TableName, loc["table-name"])
	}
}

// TestBuildTableMappings_Wildcard 验证 "%" 通配符支持。
func TestBuildTableMappings_Wildcard(t *testing.T) {
	selections := []domain.TableSelection{
		{SchemaName: "mydb", TableName: "%"},
	}
	result := dms.BuildTableMappings(selections)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	rules := out["rules"].([]interface{})
	loc := rules[0].(map[string]interface{})["object-locator"].(map[string]interface{})
	assert.Equal(t, "%", loc["table-name"])
}

// TestBuildTableMappings_Empty 验证空输入时返回空 rules 数组。
func TestBuildTableMappings_Empty(t *testing.T) {
	result := dms.BuildTableMappings(nil)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	rules, ok := out["rules"].([]interface{})
	assert.True(t, ok || out["rules"] == nil)
	if ok {
		assert.Empty(t, rules)
	}
}

// genTableSelections 生成合法的 TableSelection 切片（gopter 生成器）。
func genTableSelections() gopter.Gen {
	schemaGen := gen.AlphaString()
	tableGen := gen.OneConstOf("users", "orders", "products", "%", "my_table")
	selectionGen := gen.Struct(nil, map[string]gopter.Gen{
		"SchemaName": schemaGen,
		"TableName":  tableGen,
	})
	return gen.SliceOf(selectionGen)
}

// TestProperty7_TableMappingsCorrectness 属性测试：table-mappings 生成正确性。
// Property 7: table-mappings 生成正确性
// Validates: Requirements 4.2
func TestProperty7_TableMappingsCorrectness(t *testing.T) {
	properties := gopter.NewProperties(gopter.DefaultTestParameters())

	// 生成固定长度的 schema/table 名对
	genSchemaName := gen.OneConstOf("schema_a", "schema_b", "mydb")
	genTableName := gen.OneConstOf("users", "orders", "%", "t1")

	properties.Property("Property 7: BuildTableMappings 生成的 rules 与输入完全一致",
		prop.ForAll(
			func(schemas []string, tables []string) bool {
				// 确保长度一致
				n := len(schemas)
				if n == 0 {
					return true
				}
				if len(tables) < n {
					n = len(tables)
				}
				if n == 0 {
					return true
				}

				selections := make([]domain.TableSelection, n)
				for i := 0; i < n; i++ {
					selections[i] = domain.TableSelection{
						SchemaName: schemas[i],
						TableName:  tables[i],
					}
				}

				result := dms.BuildTableMappings(selections)

				var out map[string]interface{}
				if err := json.Unmarshal([]byte(result), &out); err != nil {
					return false
				}

				rawRules, ok := out["rules"]
				if !ok {
					return false
				}
				rules, ok := rawRules.([]interface{})
				if !ok {
					return false
				}

				// Property 7.1: rules 数量 = 输入长度
				if len(rules) != n {
					return false
				}

				// Property 7.2 & 7.3: 每条规则的 schema-name/table-name 与输入完全一致
				for i := 0; i < n; i++ {
					rule, ok := rules[i].(map[string]interface{})
					if !ok {
						return false
					}
					if rule["rule-type"] != "selection" {
						return false
					}
					loc, ok := rule["object-locator"].(map[string]interface{})
					if !ok {
						return false
					}
					if loc["schema-name"] != selections[i].SchemaName {
						return false
					}
					if loc["table-name"] != selections[i].TableName {
						return false
					}
				}
				return true
			},
			gen.SliceOfN(5, genSchemaName),
			gen.SliceOfN(5, genTableName),
		),
	)

	properties.TestingRun(t)
}
