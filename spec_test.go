package bgcodego

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	expected, err := os.ReadFile("_testdata/mini_cube_b.gcode")
	checkErr(t, err)
	const path = "_testdata/mini_cube_b.bgcode"
	fd, err := os.Open(path)
	checkErr(t, err)
	t.Cleanup(func() { fd.Close() })
	got, err := Parse(fd)
	checkErr(t, err)
	t.Log(len(string(expected)), len(got))
	if diff := cmp.Diff(string(expected), got); diff != "" {
		t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
	}
}

func checkErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
