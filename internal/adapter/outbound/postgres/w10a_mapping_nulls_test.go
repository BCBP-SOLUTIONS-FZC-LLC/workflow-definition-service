package postgres

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToPgtypeUUID_NilPointerIsNullAndAValueIsKept(t *testing.T) {
	id := uuid.New()

	assert.False(t, toPgtypeUUID(nil).Valid, "a nil pointer is stored as NULL")

	stored := toPgtypeUUID(&id)
	assert.True(t, stored.Valid)
	assert.Equal(t, pgtype.UUID{Bytes: id, Valid: true}, stored)
	back := fromPgtypeUUID(stored)
	require.NotNil(t, back)
	assert.Equal(t, id, *back)
	assert.Nil(t, fromPgtypeUUID(toPgtypeUUID(nil)), "NULL reads back as nil")
}

func TestFromJSONB_NullAndEmptyColumnsAreNilAndAValueIsKeptVerbatim(t *testing.T) {
	cases := []struct {
		name string
		in   json.RawMessage
	}{
		{"a NULL column", nil},
		{"an empty value", json.RawMessage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Nil(t, fromJSONB(tc.in))
		})
	}

	got := fromJSONB(json.RawMessage(`{"a": [1, 2]}`))
	require.NotNil(t, got)
	assert.Equal(t, `{"a": [1, 2]}`, *got)
}

func TestToNullableJSONB_NilAndEmptyAreNullAndAValueIsKept(t *testing.T) {
	empty := ""
	value := `{"k":1}`

	assert.Nil(t, toNullableJSONB(nil))
	assert.Nil(t, toNullableJSONB(&empty))
	assert.Equal(t, json.RawMessage(value), toNullableJSONB(&value))
}
