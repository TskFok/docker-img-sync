package database

import "testing"

func TestParseCredentialAndRegistry(t *testing.T) {
	tables, err := desiredTables("img_")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(tables) != 4 {
		t.Fatalf("表数量 = %d，期望 4", len(tables))
	}

	cred := tables[0]
	if cred.Name != "img_credential" {
		t.Fatalf("表名 = %s", cred.Name)
	}
	if len(cred.Columns) != 6 {
		t.Fatalf("credential 列数 = %d，期望 6", len(cred.Columns))
	}
	id := cred.Columns[0]
	if id.Name != "id" || id.TypeName != "int" || id.Nullable || !id.AutoIncrement || id.HasDefault {
		t.Fatalf("id 列 = %+v", id)
	}
	if id.Clause != "`id` int NOT NULL AUTO_INCREMENT" {
		t.Fatalf("id 子句 = %q", id.Clause)
	}
	if len(cred.PrimaryKey) != 1 || cred.PrimaryKey[0] != "id" {
		t.Fatalf("主键 = %v", cred.PrimaryKey)
	}
	if len(cred.Indexes) != 1 || cred.Indexes[0].Name != "uk_credential_name" || !cred.Indexes[0].Unique || cred.Indexes[0].Columns[0] != "name" {
		t.Fatalf("索引 = %+v", cred.Indexes)
	}

	reg := tables[1]
	if len(reg.ForeignKeys) != 1 {
		t.Fatalf("registry 外键数量 = %d", len(reg.ForeignKeys))
	}
	fk := reg.ForeignKeys[0]
	if fk.Name != "fk_registry_credential" || fk.RefTable != "img_credential" || fk.OnDelete != "RESTRICT" || fk.Columns[0] != "credential_id" || fk.RefColumns[0] != "id" {
		t.Fatalf("外键 = %+v", fk)
	}
	if fk.Clause != "CONSTRAINT `fk_registry_credential` FOREIGN KEY (`credential_id`) REFERENCES `img_credential` (`id`) ON DELETE RESTRICT" {
		t.Fatalf("外键子句 = %q", fk.Clause)
	}
}

func TestParseSyncTaskDefaultsAndIndex(t *testing.T) {
	tables, err := desiredTables("")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	task := tables[2]
	cols := map[string]columnSpec{}
	for _, col := range task.Columns {
		cols[col.Name] = col
	}
	dest := cols["dest_repository"]
	if !dest.HasDefault || dest.DefaultValue != "" || dest.Nullable || dest.TypeName != "varchar(255)" {
		t.Fatalf("dest_repository = %+v", dest)
	}
	interval := cols["interval_seconds"]
	if !interval.HasDefault || interval.DefaultValue != "0" {
		t.Fatalf("interval_seconds = %+v", interval)
	}
	enabled := cols["enabled"]
	if !enabled.HasDefault || enabled.DefaultValue != "1" || enabled.TypeName != "tinyint" {
		t.Fatalf("enabled = %+v", enabled)
	}
	status := cols["last_status"]
	if status.DefaultValue != "idle" {
		t.Fatalf("last_status 默认值 = %q", status.DefaultValue)
	}
	phase := cols["progress_phase"]
	if phase.TypeName != "varchar(16)" || phase.Nullable || !phase.HasDefault || phase.DefaultValue != "" {
		t.Fatalf("progress_phase = %+v", phase)
	}
	sourceCred := cols["source_credential_id"]
	if !sourceCred.Nullable || sourceCred.HasDefault {
		t.Fatalf("source_credential_id = %+v", sourceCred)
	}
	if len(task.Indexes) != 1 || task.Indexes[0].Name != "idx_sync_task_due" || task.Indexes[0].Unique {
		t.Fatalf("索引 = %+v", task.Indexes)
	}
	if len(task.Indexes[0].Columns) != 2 || task.Indexes[0].Columns[0] != "enabled" || task.Indexes[0].Columns[1] != "next_run_at" {
		t.Fatalf("索引列 = %v", task.Indexes[0].Columns)
	}
	logFK := tables[3].ForeignKeys[0]
	if logFK.OnDelete != "CASCADE" || logFK.RefTable != "sync_task" {
		t.Fatalf("sync_log 外键 = %+v", logFK)
	}
}
