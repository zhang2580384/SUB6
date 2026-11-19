package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Hold one data write inside the actual WebSocket transport. Canceling its
// context closes the underlying connection, just as it does for a slow peer.
type wsCloseWriteGate struct {
	net.Listener
	armed                   atomic.Bool
	writing, resume, closed chan struct{}
	closeOnce               sync.Once
}

type wsCloseGatedConn struct {
	net.Conn
	gate *wsCloseWriteGate
}

func (g *wsCloseWriteGate) Accept() (net.Conn, error) {
	conn, err := g.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &wsCloseGatedConn{conn, g}, nil
}
func (c *wsCloseGatedConn) Write(p []byte) (int, error) {
	if c.gate.armed.Swap(false) {
		close(c.gate.writing)
		select {
		case <-c.gate.resume:
		case <-c.gate.closed:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}
func (c *wsCloseGatedConn) Close() error {
	c.gate.closeOnce.Do(func() { close(c.gate.closed) })
	return c.Conn.Close()
}

func TestOpenAIWSStreamCloseDuringDownstreamWrite(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		shared       bool
	}{
		{"legacy-alone", "", false}, {"legacy-with-sibling", "", true}, {"named-with-sibling", "first", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			accepted := make(chan *coderws.Conn, 1)
			serverDone := make(chan struct{})
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				conn, err := coderws.Accept(w, req, nil)
				if err != nil {
					t.Errorf("accept websocket: %v", err)
					return
				}
				defer conn.CloseNow()
				accepted <- conn
				<-serverDone
			}))
			gate := &wsCloseWriteGate{Listener: srv.Listener, writing: make(chan struct{}), resume: make(chan struct{}), closed: make(chan struct{})}
			srv.Listener = gate
			srv.Start()
			defer srv.Close()
			defer close(serverDone)
			resume := sync.OnceFunc(func() { close(gate.resume) })
			defer resume()
			peer, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			require.NoError(t, err)
			defer peer.CloseNow()
			var transport *coderws.Conn
			select {
			case transport = <-accepted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			router := &openAIWSStreamRouter{conn: transport, ctx: ctx, sessions: make(map[string]*openAIWSLogicalSession)}
			newLane := func(key, stream string) *openAIWSLogicalSession {
				laneCtx, stop := context.WithCancel(ctx)
				t.Cleanup(stop)
				lane := &openAIWSLogicalSession{router: router, key: key, streamID: stream, ctx: laneCtx, cancel: stop, closed: make(chan struct{}), busy: true}
				router.sessions[key] = lane
				return lane
			}
			lane := newLane("first", tc.stream)
			var sibling *openAIWSLogicalSession
			if tc.shared {
				sibling = newLane("second", "second")
			}
			const data = `{"type":"response.output_text.delta","delta":"fixture"}`
			const reason = "fixture finished"
			writeDone := make(chan error, 1)
			closeDone := make(chan error, 1)
			gate.armed.Store(true)
			go func() { writeDone <- lane.Write(lane.ctx, coderws.MessageText, []byte(data)) }()
			select {
			case <-gate.writing:
			case <-ctx.Done():
				t.Fatal("data write did not start")
			}
			go func() { closeDone <- lane.Close(coderws.StatusPolicyViolation, reason) }()
			select {
			case <-lane.closed:
			case <-ctx.Done():
				t.Fatal("lane did not begin draining")
			}
			if lane.ctx.Err() != nil {
				// If cleanup canceled the active write, observe the resulting
				// transport shutdown before releasing the writer. No timing sleeps.
				select {
				case <-gate.closed:
				case <-ctx.Done():
					t.Fatal("canceled write never settled")
				}
			}
			resume()
			_, frame, err := peer.Read(ctx)
			dataDelivered := err == nil
			if dataDelivered {
				require.Equal(t, "fixture", gjson.GetBytes(frame, "delta").String())
				_, frame, err = peer.Read(ctx)
			}
			if !tc.shared {
				var ce coderws.CloseError
				require.ErrorAs(t, err, &ce, "peer must receive a close frame, not EOF")
				require.Equal(t, coderws.StatusPolicyViolation, ce.Code)
				require.Equal(t, reason, ce.Reason)
				// The physical reader can still dispatch a frame already buffered during the handshake.
				require.NoError(t, router.route(coderws.MessageText, []byte(`{"type":"response.create","stream_id":"late"}`), time.Now()))
				require.Len(t, router.sessions, 1)
			} else {
				require.NoError(t, err, "a closing lane must leave its sibling's socket open")
				require.Equal(t, "websocket_session_closed", gjson.GetBytes(frame, "error.code").String())
				require.Equal(t, reason, gjson.GetBytes(frame, "error.message").String())
				require.Equal(t, tc.stream, gjson.GetBytes(frame, "stream_id").String())
				require.NoError(t, sibling.Write(sibling.ctx, coderws.MessageText, []byte(data)))
				_, frame, err = peer.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, "second", gjson.GetBytes(frame, "stream_id").String())
			}
			require.True(t, dataDelivered, "the peer must receive the already accepted data frame")
			select {
			case err = <-writeDone:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("write never settled")
			}
			select {
			case err = <-closeDone:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("close never settled")
			}
			require.ErrorIs(t, lane.ctx.Err(), context.Canceled)
		})
	}
}
