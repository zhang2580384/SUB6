package middleware

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestAmbiguousFieldsAdmissionDecodesOnce(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", "x-gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			doc := []byte(`{"model":"example","messages":[{"role":"user","content":"hello"}]}`)
			var wire bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip", "x-gzip":
				writer = gzip.NewWriter(&wire)
			case "deflate":
				writer = zlib.NewWriter(&wire)
			case "zstd":
				var err error
				writer, err = zstd.NewWriter(&wire)
				require.NoError(t, err)
			}
			raw := append([]byte{0xef, 0xbb, 0xbf}, doc...)
			if writer == nil {
				wire.Write(raw)
			} else {
				_, err := writer.Write(raw)
				require.NoError(t, err)
				require.NoError(t, writer.Close())
			}
			router := gin.New()
			router.POST("/v1/messages", RequestBodyLimit(1<<20), RejectConflictingRequestFields(), func(c *gin.Context) {
				data, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, doc, data)
				require.EqualValues(t, len(doc), c.Request.ContentLength)
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", &wire)
			req.Header.Set("Content-Encoding", encoding)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNoContent, rec.Code)
		})
	}
}

func TestAmbiguousFieldsAdmissionNestedAndMultipart(t *testing.T) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	require.NoError(t, form.WriteField("image_url", "example-one"))
	require.NoError(t, form.WriteField("image_url", "example-two"))
	require.NoError(t, form.Close())
	for _, tc := range []struct {
		path, ct, body string
		status         int
	}{
		{"/v1/responses", "application/json", `{"options":{"label":"one","label":"two"}}`, 400},
		{"/v1/messages", "application/json", `{"tools":[{"input_schema":{"properties":{"Name":{},"name":{}}}}]}`, 204},
		{"/v1beta/models/example", "application/json", `{"options":{"label":"one","label":"two"}}`, 400},
		{"/v1/images/edits", form.FormDataContentType(), buf.String(), 204},
	} {
		router := gin.New()
		handled := false
		router.POST(tc.path, RequestBodyLimit(1<<20), RejectConflictingRequestFields(), func(c *gin.Context) { handled = true; c.Status(204) })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", tc.ct)
		router.ServeHTTP(rec, req)
		require.Equal(t, tc.status, rec.Code, tc.path)
		require.Equal(t, tc.status == 204, handled)
	}
}

func TestGatewayReadLimitUsesProtocolAndEffectiveSize(t *testing.T) {
	for _, test := range []struct{ route, url, protocol string }{
		{"/v1/messages", "/v1/messages", "anthropic"},
		{"/antigravity/v1/messages/count_tokens", "/antigravity/v1/messages/count_tokens", "anthropic"},
		{"/v1/systemone", "/v1/systemone", "anthropic"},
		{"/v1/embeddings", "/v1/embeddings", "openai"},
		{"/backend-api/codex/alpha/search", "/backend-api/codex/alpha/search", "openai"},
		{"/v1/responses/*subpath", "/v1/responses/messages", "openai"},
		{"/v1beta/models/*action", "/v1beta/models/example:generateContent", "google"},
	} {
		for _, encoding := range []string{"identity", "gzip"} {
			t.Run(test.url+"/"+encoding, func(t *testing.T) {
				const limit = 128
				data := bytes.Repeat([]byte("x"), limit+1)
				var wire bytes.Buffer
				if encoding == "gzip" {
					w := gzip.NewWriter(&wire)
					_, err := w.Write(data)
					require.NoError(t, err)
					require.NoError(t, w.Close())
					require.Less(t, wire.Len(), limit)
				} else {
					wire.Write(data)
				}
				router := gin.New()
				router.POST(test.route, RequestBodyLimit(limit), RejectConflictingRequestFields(), func(*gin.Context) { t.Fatal("oversize request reached the handler") })
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, test.url, &wire)
				req.Header.Set("Content-Encoding", encoding)
				router.ServeHTTP(rec, req)
				require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
				var resp struct {
					Type  string
					Error struct {
						Type, Status, Message string
						Code                  int
					}
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Equal(t, "Request body too large, limit is 128B", resp.Error.Message)
				switch test.protocol {
				case "google":
					require.Equal(t, 413, resp.Error.Code)
					require.NotEmpty(t, resp.Error.Status)
				case "anthropic":
					require.Equal(t, "error", resp.Type)
					require.Equal(t, "invalid_request_error", resp.Error.Type)
				case "openai":
					require.Empty(t, resp.Type)
					require.Equal(t, "invalid_request_error", resp.Error.Type)
				}
			})
		}
	}
}

func TestGatewayReadFailureUsesMessagesProtocol(t *testing.T) {
	router := gin.New()
	router.POST("/v1/messages", RequestBodyLimit(1024), RejectConflictingRequestFields(), func(*gin.Context) { t.Fatal("failed read reached handler") })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString("an incomplete upload"))
	req.Header.Set("Content-Encoding", "gzip")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"invalid_request_error","message":"Failed to read request body"}}`, rec.Body.String())
}

func TestGatewayBodyCapacityErrorsUseMatchedProtocol(t *testing.T) {
	const message = "Request body buffering capacity is exhausted; retry later"
	for _, tc := range []struct{ route, url, response string }{
		{"/v1/systemone", "/v1/systemone", `{"type":"error","error":{"type":"api_error","message":"` + message + `"}}`},
		{"/v1/messages", "/v1/messages", `{"type":"error","error":{"type":"api_error","message":"` + message + `"}}`},
		{"/v1/responses/*subpath", "/v1/responses/messages", `{"error":{"type":"server_error","message":"` + message + `"}}`},
		{"/v1beta/models/*action", "/v1beta/models/example:generateContent", `{"error":{"code":503,"status":"UNAVAILABLE","message":"` + message + `"}}`},
	} {
		t.Run(tc.url, func(t *testing.T) {
			router := gin.New()
			router.POST(tc.route, RequestBodyLimit(1024, pkghttputil.NewBodyMemoryBudget(1)), RejectConflictingRequestFields(), func(*gin.Context) { t.Fatal("capacity error reached handler") })
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.url, bytes.NewBufferString(`{"model":"example"}`))
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Equal(t, "1", rec.Header().Get("Retry-After"))
			require.JSONEq(t, tc.response, rec.Body.String())
		})
	}
}
