package music

import "fmt"

type backupSchemaError struct{ Source, Target int }

func (e *backupSchemaError) Error() string {
	if e.Source == 7 && e.Target >= 9 {
		return "此 schema 7 备份需要先在隔离的兼容 schema 8 历史程序恢复，再按 schema 8 升级流程处理；不是清单损坏"
	}
	return fmt.Sprintf("备份 schema %d 不支持直接恢复到 schema %d，请使用兼容版本离线处理", e.Source, e.Target)
}
