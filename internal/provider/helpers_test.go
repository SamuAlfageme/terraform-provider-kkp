package provider

import (
	"reflect"
	"testing"
)

func TestMapPatch(t *testing.T) {
	got := mapPatch(map[string]string{"a": "1", "b": "2", "c": "3"}, map[string]string{"a": "1", "b": "20", "d": "4"})
	want := map[string]any{"b": "20", "c": nil, "d": "4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mapPatch = %v, want %v", got, want)
	}
	if got := mapPatch(nil, nil); len(got) != 0 {
		t.Errorf("mapPatch(nil, nil) = %v, want empty", got)
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("1.9.0", "1.10.0") >= 0 || compareVersions("1.32.5", "1.32.5") != 0 || compareVersions("2.0.0", "1.99.9") <= 0 {
		t.Error("compareVersions does not compare numerically")
	}
}

func TestSplitID(t *testing.T) {
	if _, err := splitID("a/b", 3, "x/y/z"); err == nil {
		t.Error("want an error for too few parts")
	}
	if _, err := splitID("a//c", 3, "x/y/z"); err == nil {
		t.Error("want an error for an empty part")
	}
	if p, err := splitID("a/b/c", 3, "x/y/z"); err != nil || p[2] != "c" {
		t.Errorf("got %v, %v", p, err)
	}
}
