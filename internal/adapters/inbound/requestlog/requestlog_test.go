package requestlog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/claudioed/outbound-shipping/internal/adapters/inbound/requestlog"
)

func serve(t *testing.T, status int, target string) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestlog.RequestLogger(logger))
	r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v (%q)", err, buf.String())
	}
	return line
}

func TestRequestLogger_WritesOneStructuredLinePerRequest(t *testing.T) {
	line := serve(t, http.StatusOK, "/loads/load-1")

	want := map[string]any{
		"msg": "http request", "level": "INFO", "method": "GET",
		"path": "/loads/load-1", "status": float64(200), "bytes": float64(4),
	}
	for key, value := range want {
		if line[key] != value {
			t.Errorf("%s = %v, want %v", key, line[key], value)
		}
	}
	for _, key := range []string{"duration_ms", "request_id"} {
		if _, ok := line[key]; !ok {
			t.Errorf("log line has no %s", key)
		}
	}
}

func TestRequestLogger_ServerErrorsAreLoggedAtError(t *testing.T) {
	line := serve(t, http.StatusInternalServerError, "/loads/load-1")

	if line["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR for a 5xx", line["level"])
	}
}

// CWE-117: a CR/LF smuggled into the path must not survive into the log.
func TestRequestLogger_StripsLineBreaksFromThePath(t *testing.T) {
	line := serve(t, http.StatusOK, "/loads/a%0d%0aFORGED")

	path, _ := line["path"].(string)
	if strings.ContainsAny(path, "\r\n") {
		t.Errorf("path = %q still carries a line break", path)
	}
}
