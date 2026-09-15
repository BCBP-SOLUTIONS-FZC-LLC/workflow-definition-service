package mapping_test

import (
	"testing"
)

func TestPostgresMappingHelpers(t *testing.T) {
	t.Skip("Postgres mapping functions are currently private/unexported in internal/adapter/outbound/postgres/mapping.go. " +
		"They are tested via internal/adapter/outbound/postgres/mapping_test.go. " +
		"This placeholder will be activated if mapping helpers are exposed or refactored to a shared package.")
}
