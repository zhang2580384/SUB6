package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type streamAuthKeyRepo struct {
	service.APIKeyRepository
	key      *service.APIKey
	disabled atomic.Bool
}

func (r *streamAuthKeyRepo) GetByKeyForAuth(_ context.Context, credential string) (*service.APIKey, error) {
	if credential != r.key.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	key, user, group := *r.key, *r.key.User, *r.key.Group
	key.User, key.Group = &user, &group
	if r.disabled.Load() {
		key.Status = service.StatusAPIKeyDisabled
	}
	return &key, nil
}

func (*streamAuthKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

type streamAuthLeaseCache struct {
	service.ConcurrencyCache
	service.APIKeySlotLeaseCache
	admission   service.APIKeySlotAdmissionCache
	mu          sync.Mutex
	firstID     string
	expireFirst atomic.Bool
	permissions chan *service.APIKeyQueueImagePermission
}

func (c *streamAuthLeaseCache) AcquireAPIKeySlot(ctx context.Context, key int64, limit int, id string) (bool, error) {
	acquired, err := c.admission.AcquireAPIKeySlot(ctx, key, limit, id)
	if acquired && err == nil {
		c.mu.Lock()
		if c.firstID == "" {
			c.firstID = id
		}
		c.mu.Unlock()
		c.permissions <- service.APIKeyQueueImagePermissionFromContext(ctx)
	}
	return acquired, err
}

func (c *streamAuthLeaseCache) RefreshAPIKeySlot(ctx context.Context, key int64, id string) (bool, error) {
	c.mu.Lock()
	first := c.firstID == id
	c.mu.Unlock()
	if first && c.expireFirst.Load() {
		return false, nil
	}
	return c.APIKeySlotLeaseCache.RefreshAPIKeySlot(ctx, key, id)
}

func (*streamAuthLeaseCache) APIKeySlotRefreshInterval() time.Duration { return 10 * time.Millisecond }
func (*streamAuthLeaseCache) APIKeySlotTTL() time.Duration             { return 3 * time.Second }

// Exercise the real authentication middleware, its revalidator and renewable
// Redis admission over a real socket. A failed logical lease must stop its own
// detached HTTP upstream without killing the socket, sibling or auth snapshot.
func TestOpenAIWSStreamRealAuthLeaseLossIsIsolatedAndRevocationStillApplies(t *testing.T) {
	alphaStarted, alphaStopped := make(chan struct{}), make(chan struct{})
	betaStarted, releaseBeta := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if gjson.GetBytes(body, "input").String() == "alpha" {
			close(alphaStarted)
			<-r.Context().Done()
			close(alphaStopped)
			return
		}
		close(betaStarted)
		select {
		case <-releaseBeta:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_auth_beta\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer up.Close()
	defer releaseOnce.Do(func() { close(releaseBeta) })
	h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, nil)
	require.NoError(t, h.clientConn.CloseNow())
	select {
	case <-h.handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("initial harness did not stop")
	}
	baseCache := testutil.NewRedisConcurrencyCache(t)
	cache := &streamAuthLeaseCache{ConcurrencyCache: baseCache,
		APIKeySlotLeaseCache: baseCache.(service.APIKeySlotLeaseCache), admission: baseCache.(service.APIKeySlotAdmissionCache),
		permissions: make(chan *service.APIKeyQueueImagePermission, 8)}
	h.handler.concurrencyHelper = NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Millisecond)
	key := *h.apiKey
	user := *key.User
	user.Concurrency = 8
	key.User = &user
	key.Status, key.ConcurrencyLimit = service.StatusActive, 8
	key.Group = &service.Group{ID: *key.GroupID, Status: service.StatusActive, Platform: service.PlatformOpenAI, Hydrated: true}
	repo := &streamAuthKeyRepo{key: &key}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	keySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	physical := make(chan context.Context, 1)
	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keySvc, nil, cfg)))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		physical <- c.Request.Context()
		h.handler.ResponsesWebSocket(c)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key.Key}}})
	require.NoError(t, err)
	defer conn.CloseNow()
	physicalCtx := <-physical
	streamIsolationSend(t, conn, streamIsolationPayload("alpha", "thread-alpha", "alpha", ""))
	select {
	case <-alphaStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated alpha did not start")
	}
	streamIsolationSend(t, conn, streamIsolationPayload("beta", "thread-beta", "beta", ""))
	select {
	case <-betaStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated beta did not start")
	}
	alphaPermission, betaPermission := <-cache.permissions, <-cache.permissions
	require.NotNil(t, alphaPermission)
	require.NotSame(t, alphaPermission, betaPermission)
	require.NotSame(t, alphaPermission, service.APIKeyQueueImagePermissionFromContext(physicalCtx))
	_, known := service.APIKeyQueueImagePermissionFromContext(physicalCtx).Allowed()
	require.False(t, known, "logical revalidation must not mutate the physical auth result")
	cache.expireFirst.Store(true)
	event := streamIsolationRead(t, conn)
	require.Equal(t, "alpha", gjson.GetBytes(event, "stream_id").String())
	require.Equal(t, "error", gjson.GetBytes(event, "type").String())
	require.NoError(t, physicalCtx.Err(), "a logical capacity failure must not cancel physical authentication")
	select {
	case <-alphaStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("lost lease did not stop detached alpha upstream")
	}
	releaseOnce.Do(func() { close(releaseBeta) })
	event = streamIsolationTerminal(t, conn)
	require.Equal(t, "beta", gjson.GetBytes(event, "stream_id").String())
	// Sharing the immutable revalidator must not disable fresh turn checks.
	repo.disabled.Store(true)
	streamIsolationSend(t, conn, streamIsolationPayload("beta", "thread-beta", "revoked", ""))
	event = streamIsolationRead(t, conn)
	require.Equal(t, "beta", gjson.GetBytes(event, "stream_id").String())
	require.Equal(t, "error", gjson.GetBytes(event, "type").String())
	require.EqualValues(t, 2, calls.Load(), "revoked continuation must never reach upstream")
}
