package http

import (
	"bytes"
	"net/http"
)

// capture records the handler's response so it can be stored (and committed)
// BEFORE anything reaches the client. It is also what the error writer marks
// with the replay class of the problem it answers (replayMarker).
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
	// class is the replay class the error writer marked; empty when the handler
	// answered without a problem (a 2xx) or with one that declares none.
	class replayClass
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

// MarkReplay implements replayMarker.
func (c *capture) MarkReplay(class replayClass) { c.class = class }

// succeeded reports whether the handler answered 2xx: the only answer stored
// with its effects. Anything else rolls the unit of work back.
func (c *capture) succeeded() bool {
	return c.code >= http.StatusOK && c.code < http.StatusMultipleChoices
}

// remembersRefusal reports whether a refusal (a 4xx) is stored under the key:
// only one marked FINAL or OUTCOME. A refusal marked RETRYABLE, or one that
// reached the middleware with no class, is released: nothing is written, so a
// retry under the key executes again.
func (c *capture) remembersRefusal() bool {
	return c.class == replayFinal || c.class == replayOutcome
}

// serveCaptured runs next and returns what it answered (200 when it wrote
// nothing).
func serveCaptured(next http.Handler, r *http.Request) *capture {
	out := &capture{header: http.Header{}}
	next.ServeHTTP(out, r)
	if out.code == 0 {
		out.code = http.StatusOK
	}
	return out
}

// flush copies the captured response onto the real client connection.
func flush(w http.ResponseWriter, out *capture) {
	for k, vs := range out.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(out.code)
	_, _ = w.Write(out.body.Bytes())
}
