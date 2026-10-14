package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	inboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
)

func TestMetricsServer_HasConnectionTimeouts(t *testing.T) {
	srv := newMetricsServer(9091, http.NotFoundHandler())

	assert.Equal(t, ":9091", srv.Addr)
	assert.NotZero(t, srv.ReadHeaderTimeout, "a slow client must not hold a connection open indefinitely")
	assert.NotZero(t, srv.WriteTimeout)
	assert.NotZero(t, srv.IdleTimeout)
}

func TestHTTPServer_WriteTimeoutOutlastsTheRequestTimeout(t *testing.T) {
	srv := newHTTPServer(8080, http.NotFoundHandler())

	assert.Equal(t, ":8080", srv.Addr)
	assert.Greater(t, srv.WriteTimeout, inboundhttp.RequestTimeout,
		"at the request deadline the timeout body is still being written, so the server must not close the connection first")
	assert.NotZero(t, srv.ReadHeaderTimeout)
	assert.NotZero(t, srv.IdleTimeout)
}
