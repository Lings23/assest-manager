package taskcenter

import (
	"fmt"
	"strings"
	"testing"
)

func TestPreflightCapacity100k(t *testing.T) {
	if testing.Short() {
		t.Skip("capacity test")
	}
	var input strings.Builder
	input.Grow(2_000_000)
	input.WriteString("department_name,department_code\n")
	for i := 0; i < 100_000; i++ {
		fmt.Fprintf(&input, "部门%d,D%06d\n", i, i)
	}
	result, err := PreflightCSV("responsible-department", strings.NewReader(input.String()), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 100_000 || result.Valid != 100_000 || len(result.Errors) != 0 {
		t.Fatalf("capacity result: total=%d valid=%d errors=%d", result.Total, result.Valid, len(result.Errors))
	}
}
