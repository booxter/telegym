package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultMaxRequests = 5000

// APIRequest is a captured Bot API invocation. Multipart file contents are
// deliberately excluded; uploaded files remain available through /debug/files.
type APIRequest struct {
	Seq        int64          `json:"seq"`
	Method     string         `json:"method"`
	Parameters map[string]any `json:"parameters"`
	Status     int            `json:"status"`
	At         int64          `json:"at"`
}

func (s *Server) requestJournalMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, method, ok := parseBotPath(c.Request.URL.Path)
		if !ok {
			c.Next()
			return
		}

		parameters := captureRequestParameters(c.Request)
		sequence := s.store.NextRequestID()
		c.Next()
		s.store.Bot(token).AppendRequest(APIRequest{
			Seq:        sequence,
			Method:     method,
			Parameters: parameters,
			Status:     c.Writer.Status(),
			At:         time.Now().Unix(),
		})
	}
}

func captureRequestParameters(r *http.Request) map[string]any {
	parameters := make(map[string]any)
	for key, values := range r.URL.Query() {
		parameters[key] = collapseValues(values)
	}

	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return parameters
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var decoded map[string]any
		if json.Unmarshal(body, &decoded) == nil {
			for key, value := range decoded {
				parameters[key] = value
			}
		}
	case strings.HasPrefix(contentType, "multipart/form-data"):
		if r.ParseMultipartForm(32<<20) == nil && r.MultipartForm != nil {
			for key, values := range r.MultipartForm.Value {
				parameters[key] = collapseValues(values)
			}
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"):
		if r.ParseForm() == nil {
			for key, values := range r.PostForm {
				parameters[key] = collapseValues(values)
			}
		}
	}
	return parameters
}

func collapseValues(values []string) any {
	if len(values) == 1 {
		return values[0]
	}
	return values
}

func (b *botEntry) AppendRequest(request APIRequest) {
	b.requestMu.Lock()
	b.requests = append(b.requests, request)
	if over := len(b.requests) - b.maxRequests; over > 0 {
		copy(b.requests, b.requests[over:])
		b.requests = b.requests[:b.maxRequests]
	}
	b.requestMu.Unlock()
}

// Requests returns up to limit most-recent requests, ordered oldest to newest.
func (b *botEntry) Requests(method string, limit int) []APIRequest {
	if limit <= 0 {
		limit = 100
	}
	b.requestMu.RLock()
	out := make([]APIRequest, 0, len(b.requests))
	for _, request := range b.requests {
		if method == "" || request.Method == method {
			out = append(out, request)
		}
	}
	b.requestMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (b *botEntry) ClearRequests() int {
	b.requestMu.Lock()
	n := len(b.requests)
	b.requests = nil
	b.requestMu.Unlock()
	return n
}
