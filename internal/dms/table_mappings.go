// Package dms 实现 AWS DMS 相关逻辑：table-mappings 生成与执行编排。
package dms

import (
	"encoding/json"
	"fmt"

	"github.com/5miles/dms-access/internal/domain"
)

// BuildTableMappings 根据表选择列表生成标准 DMS selection JSON（需求 4.2, Property 7）。
//
// 每条 TableSelection 对应恰好一条 rule-type="selection" 的 inclusion 规则，
// 支持 "%" 通配符。生成规则：
//   - rule-id：从 1 开始的整数字符串
//   - rule-name：sync-rule-{n}
//   - rule-type：selection
//   - rule-action：include
//   - object-locator.schema-name：sel.SchemaName
//   - object-locator.table-name：sel.TableName（可含 "%"）
func BuildTableMappings(selections []domain.TableSelection) string {
	rules := make([]map[string]interface{}, 0, len(selections))
	for i, sel := range selections {
		rules = append(rules, map[string]interface{}{
			"rule-id":     fmt.Sprintf("%d", i+1),
			"rule-name":   fmt.Sprintf("sync-rule-%d", i+1),
			"rule-type":   "selection",
			"rule-action": "include",
			"object-locator": map[string]string{
				"schema-name": sel.SchemaName,
				"table-name":  sel.TableName,
			},
			"filters": []interface{}{},
		})
	}
	result, _ := json.Marshal(map[string]interface{}{"rules": rules})
	return string(result)
}
