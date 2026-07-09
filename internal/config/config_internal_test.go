package config

import (
	"testing"
	"time"
)

func TestGetEnvIntOrDefault_InvalidValue(t *testing.T) {
	t.Setenv("TEST_INT_VAR", "not-an-int")
	if got := getEnvIntOrDefault("TEST_INT_VAR", 42); got != 42 {
		t.Errorf("getEnvIntOrDefault() = %d, want fallback 42", got)
	}
}

func TestGetEnvFloat64OrDefault_InvalidValue(t *testing.T) {
	t.Setenv("TEST_FLOAT_VAR", "not-a-float")
	if got := getEnvFloat64OrDefault("TEST_FLOAT_VAR", 1.5); got != 1.5 {
		t.Errorf("getEnvFloat64OrDefault() = %v, want fallback 1.5", got)
	}
}

func TestGetEnvDurationOrDefault_InvalidValue(t *testing.T) {
	t.Setenv("TEST_DURATION_VAR", "not-a-duration")
	want := 30 * time.Second
	if got := getEnvDurationOrDefault("TEST_DURATION_VAR", want); got != want {
		t.Errorf("getEnvDurationOrDefault() = %v, want fallback %v", got, want)
	}
}
