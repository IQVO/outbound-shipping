package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// maxBodyBytes bounds a request body; every body here is a few hundred bytes.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 7807 application/problem+json body. Code is the
// stable fleet error code of apis/openapi.yaml (the Problem schema's
// required `code`).
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance"`
}

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON strictly decodes r's body into dest: an empty body, unknown
// fields, trailing data and malformed JSON are errMalformedRequest.
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

// writeError writes err as its RFC 7807 problem. An unmapped error is a 500
// whose detail never leaks the internal message (it is logged instead).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", logSafe(err.Error()), "method", logSafe(r.Method), "path", logSafe(r.URL.Path))
		writeProblem(w, r, p, "an unexpected error occurred")
		return
	}
	detail := p.detail
	if detail == "" {
		detail = err.Error()
	}
	writeProblem(w, r, p, detail)
}

// replayMarker is implemented by the Idempotency-Key middleware's capturing
// writer: the error writer tells it the replay class of the problem it is about
// to answer, so the middleware reads the class and keeps no list of slugs.
type replayMarker interface{ MarkReplay(class replayClass) }

// writeProblem writes p with an explicit detail and marks the capturing writer
// (if w is one) with p's replay class: every problem on every route, writeError
// included, goes through here.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	if m, ok := w.(replayMarker); ok {
		m.MarkReplay(p.class)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:     problemBaseURI + p.slug,
		Title:    p.title,
		Status:   p.status,
		Code:     p.slug,
		Detail:   detail,
		Instance: r.URL.EscapedPath(),
	})
}

// recoverPanic turns a handler panic into the 500 problem instead of a dropped
// connection. http.ErrAbortHandler keeps its net/http meaning.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			slog.ErrorContext(r.Context(), "handler panicked", "panic", rec, "method", logSafe(r.Method), "path", logSafe(r.URL.Path))
			writeProblem(w, r, internalProblem, "an unexpected error occurred")
		}()
		next.ServeHTTP(w, r)
	})
}
