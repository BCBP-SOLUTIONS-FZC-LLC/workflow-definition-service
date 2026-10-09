package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetricsServer_HasConnectionTimeouts(t *testing.T) {
	srv := newMetricsServer(9091, http.NotFoundHandler())

	assert.Equal(t, ":9091", srv.Addr)
	assert.NotZero(t, srv.ReadHeaderTimeout, "a slow client must not hold a connection open indefinitely")
	assert.NotZero(t, srv.WriteTimeout)
	assert.NotZero(t, srv.IdleTimeout)
}
