package mediaindex

import (
	"strings"
	"testing"
)

func TestOwnedMediaPathsOnly(t *testing.T) {
	good := "/sdcard/Pictures/mobile_autorun_33d06875-9ebf-4d2a-b5c5-3cffac42903f/01.png"
	if e := ValidatePaths([]string{good}); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/sdcard/Pictures/personal.png", good + ";id", good + "'", strings.Replace(good, "/01.png", "/../01.png", 1)} {
		if ValidatePaths([]string{p}) == nil {
			t.Fatalf("unsafe path accepted %s", p)
		}
	}
	if ValidatePaths([]string{good, good}) == nil {
		t.Fatal("duplicate accepted")
	}
	if ValidatePaths([]string{good, strings.Replace(good, "33d06875", "23d06875", 1)}) == nil {
		t.Fatal("mixed tasks accepted")
	}
}
