package main

import (
	"reflect"
	"testing"
)

func TestNormalizeSelfRestartTimes(t *testing.T) {
	got := normalizeSelfRestartTimes([]string{"6:00:00", "06:00", " 14:30 ", "24:00", "bad", "14:30"})
	want := []string{"06:00", "14:30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeSelfRestartTimes() = %v, want %v", got, want)
	}
}

func TestValidateSelfRestart(t *testing.T) {
	if err := validateSelfRestart(); err != nil {
		t.Fatalf("validateSelfRestart failed: %v", err)
	}
}
