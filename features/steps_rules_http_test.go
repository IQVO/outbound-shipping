package features_test

// REST vocabulary of the rule features: every call goes through the real
// chi router with the Idempotency-Key middleware, over the in-memory doubles.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

const problemBase = "https://errors.outbound-shipping.warehouse-systems.dev/"

// send performs one request on the router and remembers the answer.
func (w *world) send(method, path, body string, header map[string]string) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	w.svc.router.ServeHTTP(rec, req)
	w.rules.resp = rec
	w.rules.body = nil
	var v any
	if json.Unmarshal(rec.Body.Bytes(), &v) == nil {
		w.rules.body = v
	}
}

// post sends a POST with a fresh Idempotency-Key.
func (w *world) post(path string, body any) error {
	raw := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	w.send(http.MethodPost, path, raw, map[string]string{
		"Idempotency-Key": fmt.Sprintf("rules-request-%04d", w.nextSeq()),
	})
	return nil
}

func (w *world) nextSeq() int {
	w.rules.seq++
	return w.rules.seq
}

func registerRulesHTTPSteps(sc *godog.ScenarioContext, w *world) {
	registerPackageCommandSteps(sc, w)
	registerLoadCommandSteps(sc, w)
	registerRawRequestSteps(sc, w)
	registerReadinessSteps(sc, w)
	registerResponseAssertions(sc, w)
}

func registerReadinessSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the "(postgres|kafka)" dependency is unreachable$`, func(name string) error {
		w.svc.depsDown[name] = true
		return nil
	})

	sc.Step(`^the service starts draining$`, func() error {
		w.svc.readiness.SetNotReady()
		return nil
	})
}

func registerPackageCommandSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the dock operator scans package "([^"]*)" into "([^"]*)"$`, func(pkg, loc string) error {
		return w.post("/packages/"+pkg+"/stagings", map[string]string{"staging_location": loc})
	})

	sc.Step(`^the dock operator scans package "([^"]*)" onto load "([^"]*)" through door "([^"]*)"$`,
		func(pkg, load, door string) error {
			return w.post("/packages/"+pkg+"/loadings", map[string]string{"load_id": load, "door_code": door})
		})

	sc.Step(`^the dock operator takes package "([^"]*)" back off the load(?: because "([^"]*)")?$`,
		func(pkg, reason string) error {
			body := map[string]string{}
			if reason != "" {
				body["reason"] = reason
			}
			return w.post("/packages/"+pkg+"/unloadings", body)
		})
}

func registerLoadCommandSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the supervisor closes load "([^"]*)"$`, func(load string) error {
		return w.post("/loads/"+load+"/closures", map[string]any{})
	})

	sc.Step(`^the supervisor closes load "([^"]*)" short-shipping package "([^"]*)" because "([^"]*)"$`,
		func(load, pkg, reason string) error {
			return w.post("/loads/"+load+"/closures", map[string]any{
				"short_ships": []map[string]string{{"package_id": pkg, "reason": reason}},
			})
		})

	sc.Step(`^the supervisor reopens load "([^"]*)"$`, func(load string) error {
		return w.post("/loads/"+load+"/reopenings", map[string]any{})
	})

	sc.Step(`^the supervisor cancels load "([^"]*)" because "([^"]*)"$`, func(load, reason string) error {
		return w.post("/loads/"+load+"/cancellations", map[string]string{"reason": reason})
	})

	sc.Step(`^the supervisor voids bill of lading "([^"]*)" because "([^"]*)"$`, func(bol, reason string) error {
		return w.post("/bills-of-lading/"+bol+"/voids", map[string]string{"reason": reason})
	})

	sc.Step(`^the scheduler sweeps site "([^"]*)"(?: at cutoff "([^"]*)")?$`, func(site, cutoff string) error {
		body := map[string]string{"site_id": site}
		if cutoff != "" {
			body["cutoff"] = cutoff
		}
		return w.post("/missed-load-sweeps", body)
	})
}

func registerRawRequestSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^GET "([^"]*)" is requested$`, func(path string) error {
		w.send(http.MethodGet, path, "", nil)
		return nil
	})

	sc.Step(`^the next page of "([^"]*)" is requested with limit (\d+)$`, func(path string, limit int) error {
		m, _ := w.rules.body.(map[string]any)
		cursor, _ := m["nextCursor"].(string)
		if cursor == "" {
			return fmt.Errorf("the last page has no nextCursor: %s", w.rules.resp.Body.String())
		}
		if first, ok := lookup(m, "items.0.id"); ok {
			w.rules.prevID = render(first)
		}
		w.send(http.MethodGet, fmt.Sprintf("%s?limit=%d&cursor=%s", path, limit, cursor), "", nil)
		return nil
	})

	sc.Step(`^the page does not repeat the previous page's first item$`, func() error {
		got, _ := lookup(w.rules.body, "items.0.id")
		if render(got) == w.rules.prevID || w.rules.prevID == "" {
			return fmt.Errorf("item %v repeats the previous page (%q)", got, w.rules.prevID)
		}
		return nil
	})

	body := func(raw string) string {
		if raw == "(empty)" {
			return ""
		}
		return raw
	}

	sc.Step(`^a POST is sent to "([^"]*)" with the body (.+) and the Idempotency-Key "([^"]*)"$`,
		func(path, raw, key string) error {
			w.send(http.MethodPost, path, body(raw), map[string]string{"Idempotency-Key": key})
			w.rememberFirst(key)
			return nil
		})

	sc.Step(`^a POST is sent to "([^"]*)" with the body (.+) and no Idempotency-Key$`, func(path, raw string) error {
		w.send(http.MethodPost, path, body(raw), nil)
		return nil
	})

	sc.Step(`^a POST is sent to "([^"]*)" with the body (.+) and an Idempotency-Key of (\d+) characters$`,
		func(path, raw string, n int) error {
			w.send(http.MethodPost, path, body(raw), map[string]string{"Idempotency-Key": strings.Repeat("k", n)})
			return nil
		})
}

// rememberFirst keeps the first answer sent under a key, for the byte-identical
// replay assertion.
func (w *world) rememberFirst(key string) {
	if w.rules.byKey == nil {
		w.rules.byKey = map[string]string{}
	}
	if _, seen := w.rules.byKey[key]; !seen {
		w.rules.byKey[key] = w.rules.resp.Body.String()
	}
}

// --- response assertions ---------------------------------------------------

// lookup walks a dotted path ("short_ships.0.reason") through the decoded body.
func lookup(root any, path string) (any, bool) {
	cur := root
	if path == "" {
		return cur, true
	}
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// render prints a JSON scalar the way a feature file writes it.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func (w *world) answer() (*httptest.ResponseRecorder, error) {
	if w.rules.resp == nil {
		return nil, errors.New("no REST answer yet")
	}
	return w.rules.resp, nil
}

func registerResponseAssertions(sc *godog.ScenarioContext, w *world) {
	registerStatusAssertions(sc, w)
	registerFieldAssertions(sc, w)
}

func registerStatusAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response status is (\d+)$`, w.assertStatus)
	sc.Step(`^the problem is "([^"]*)" with status (\d+)$`, w.assertProblem)
	sc.Step(`^the problem detail mentions "([^"]*)"$`, w.assertProblemDetail)
	sc.Step(`^the response (was|was not) replayed from the stored answer$`, w.assertReplayed)
	sc.Step(`^the answer is byte-identical to the first one sent with the Idempotency-Key "([^"]*)"$`, w.assertSameAnswer)
}

func (w *world) assertStatus(want int) error {
	rec, err := w.answer()
	if err != nil {
		return err
	}
	if rec.Code != want {
		return fmt.Errorf("status = %d, want %d (body %s)", rec.Code, want, rec.Body.String())
	}
	return nil
}

func (w *world) assertProblem(slug string, status int) error {
	if err := w.assertStatus(status); err != nil {
		return err
	}
	if ct := w.rules.resp.Header().Get("Content-Type"); ct != "application/problem+json" {
		return fmt.Errorf("content type = %q, want application/problem+json", ct)
	}
	m, _ := w.rules.body.(map[string]any)
	if m["type"] != problemBase+slug || m["code"] != slug {
		return fmt.Errorf("problem = %v, want slug %s", m, slug)
	}
	return nil
}

func (w *world) assertProblemDetail(text string) error {
	m, _ := w.rules.body.(map[string]any)
	if d, _ := m["detail"].(string); !strings.Contains(d, text) {
		return fmt.Errorf("problem detail = %q, want it to mention %q", d, text)
	}
	return nil
}

func (w *world) assertReplayed(mode string) error {
	rec, err := w.answer()
	if err != nil {
		return err
	}
	if got := rec.Header().Get("Idempotency-Replayed") == "true"; got != (mode == "was") {
		return fmt.Errorf("Idempotency-Replayed = %v, want %v", got, mode == "was")
	}
	return nil
}

func (w *world) assertSameAnswer(key string) error {
	first, ok := w.rules.byKey[key]
	if !ok {
		return fmt.Errorf("no first answer for key %q", key)
	}
	if got := w.rules.resp.Body.String(); got != first {
		return fmt.Errorf("replayed body = %s, first body = %s", got, first)
	}
	return nil
}

func registerFieldAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, func(path, want string) error {
		got, ok := lookup(w.rules.body, path)
		if !ok {
			return fmt.Errorf("field %q is absent in %s", path, w.rules.resp.Body.String())
		}
		if render(got) != want {
			return fmt.Errorf("field %q = %s, want %s", path, render(got), want)
		}
		return nil
	})

	sc.Step(`^the response field "([^"]*)" is absent$`, func(path string) error {
		if got, ok := lookup(w.rules.body, path); ok {
			return fmt.Errorf("field %q = %s, want it absent", path, render(got))
		}
		return nil
	})

	sc.Step(`^the response list "([^"]*)" has (\d+) items?$`, func(path string, n int) error {
		got, ok := lookup(w.rules.body, path)
		list, isList := got.([]any)
		if !ok || !isList {
			return fmt.Errorf("%q is not a list in %s", path, w.rules.resp.Body.String())
		}
		if len(list) != n {
			return fmt.Errorf("%q has %d items, want %d", path, len(list), n)
		}
		return nil
	})

	sc.Step(`^the response list "([^"]*)" has an item whose "([^"]*)" is "([^"]*)"$`, func(path, field, want string) error {
		got, _ := lookup(w.rules.body, path)
		list, _ := got.([]any)
		for _, item := range list {
			if v, ok := lookup(item, field); ok && render(v) == want {
				return nil
			}
		}
		return fmt.Errorf("no item of %q has %s = %s in %s", path, field, want, w.rules.resp.Body.String())
	})
}
