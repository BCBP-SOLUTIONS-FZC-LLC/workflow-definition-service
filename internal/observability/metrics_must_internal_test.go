package observability

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMust_RegistrationError_PanicsWithThePrefixedError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantPanic string
	}{
		{
			name:      "a duplicate name",
			err:       errors.New("duplicate metrics collector registration attempted"),
			wantPanic: "metrics: register service collector: duplicate metrics collector registration attempted",
		},
		{
			name:      "an invalid name",
			err:       errors.New("invalid metric name"),
			wantPanic: "metrics: register service collector: invalid metric name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.PanicsWithValue(t, tc.wantPanic, func() { must("collector", tc.err) })
		})
	}
}

func TestMust_NoError_ReturnsTheCollector(t *testing.T) {
	require.Equal(t, "collector", must("collector", nil))
}
