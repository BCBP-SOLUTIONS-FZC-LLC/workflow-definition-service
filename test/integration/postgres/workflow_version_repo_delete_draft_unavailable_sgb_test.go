//go:build integration

package postgres_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type sgbCuttingProxy struct {
	listener net.Listener
	upstream string
	cutOn    string

	mu   sync.Mutex
	cuts int
}

func sgbStartCuttingProxy(t *testing.T, dsn, cutOnParseContaining string) (proxyDSN string, proxy *sgbCuttingProxy) {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxy = &sgbCuttingProxy{listener: listener, upstream: parsed.Host, cutOn: cutOnParseContaining}
	t.Cleanup(func() { _ = listener.Close() })
	go proxy.serve()

	parsed.Host = listener.Addr().String()
	return parsed.String(), proxy
}

func (p *sgbCuttingProxy) cutCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cuts
}

func (p *sgbCuttingProxy) serve() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.relay(client)
	}
}

func (p *sgbCuttingProxy) relay(client net.Conn) {
	server, err := net.Dial("tcp", p.upstream)
	if err != nil {
		_ = client.Close()
		return
	}
	go func() {
		_, _ = io.Copy(client, server)
		_ = client.Close()
	}()

	startup := make([]byte, 4)
	if _, err := io.ReadFull(client, startup); err != nil {
		_ = server.Close()
		return
	}
	rest := make([]byte, binary.BigEndian.Uint32(startup)-4)
	if _, err := io.ReadFull(client, rest); err != nil {
		_ = server.Close()
		return
	}
	_, _ = server.Write(append(startup, rest...))

	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(client, header); err != nil {
			_ = server.Close()
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(header[1:])-4)
		if _, err := io.ReadFull(client, payload); err != nil {
			_ = server.Close()
			return
		}
		if header[0] == 'P' && strings.Contains(string(payload), p.cutOn) {
			p.mu.Lock()
			p.cuts++
			p.mu.Unlock()
			if tcp, ok := client.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = client.Close()
			_ = server.Close()
			return
		}
		if _, err := server.Write(append(header, payload...)); err != nil {
			_ = client.Close()
			return
		}
	}
}

func TestWorkflowVersionRepo_DeleteDraft_FollowUpLookupLosesTheConnection_ReturnsUpstreamUnavailable(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	proxyDSN, proxy := sgbStartCuttingProxy(t, dsn, "SELECT id, workflow_id, tenant_id, status, bpmn_xml")
	pool, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: proxyDSN, MaxConns: 1})
	if err != nil {
		t.Fatalf("open pool through the proxy: %v", err)
	}
	t.Cleanup(pool.Close)

	err = postgres.NewWorkflowVersionRepo(pool).DeleteDraft(context.Background(), uuid.New(), uuid.New())

	if proxy.cutCount() != 1 {
		t.Fatalf("proxy cut %d connections, want exactly 1 (the version lookup after the 0-row delete)", proxy.cutCount())
	}
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("DeleteDraft = %v, want ErrUpstreamUnavailable so the API answers 503", err)
	}
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("DeleteDraft = %v, a failed lookup must not read as not-found or not-a-draft", err)
	}
}
