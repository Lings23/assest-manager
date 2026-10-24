package main

import (
	"database/sql"
	"flag"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	output := flag.String("out", "phase3-legacy.db", "fixture output path")
	flag.Parse()
	_ = os.Remove(*output)
	db, err := sql.Open("sqlite", *output)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE responsible_departments(id INTEGER PRIMARY KEY, department_name TEXT, department_code TEXT, is_deleted INTEGER);
INSERT INTO responsible_departments VALUES(900001, '第三阶段迁移样本部门', 'P3-MIGRATION', 0);
CREATE TABLE data_assets(
 id INTEGER PRIMARY KEY, source_system TEXT, security_level TEXT, data_name TEXT, data_items TEXT,
 data_carrier TEXT, processor_name TEXT, main_leader TEXT, security_leader TEXT, contact_phone TEXT,
 processing_purpose TEXT, usage_scope TEXT, cross_border TEXT, is_cross_border TEXT,
 security_measures TEXT, is_deleted INTEGER
);
INSERT INTO data_assets VALUES(
 900002, '旧业务系统', '二级', '第三阶段迁移样本数据', '姓名、联系方式', 'PostgreSQL',
 '样本处理者', '负责人甲', '负责人乙', '13800000000', '迁移验收', '内部', '否', '是', '加密', 1
);`)
	if err != nil {
		log.Fatal(err)
	}
}
