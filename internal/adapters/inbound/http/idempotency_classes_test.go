package http_test

// The replay-class rule of this service (ADR 0012), pinned beyond the fleet template
// next to it (idempotency_replay_classes_test.go, verbatim): the ADR table equals the
// catalogue, the error writer marks the class of every entry, no problem is built
// outside the constructor, no OUTCOME exists in this store, a refusal with no class is
// released, and the middleware holds no list of slugs.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
)

const adrPath = "../../../../docs/adr/0012-every-problem-type-has-a-replay-class.md"

// adrRow is `| `slug` | CLASS | status | reason |` of the ADR's class table.
var adrRow = regexp.MustCompile("^\\| `([a-z0-9-]+)` \\| (FINAL|OUTCOME|RETRYABLE) \\| (\\d{3}) \\|")

func TestTheADRDeclaresTheClassOfEveryProblemType(t *testing.T) {
	raw, err := os.ReadFile(adrPath)
	if err != nil {
		t.Fatalf("read the ADR: %v", err)
	}
	type row struct{ class, status string }
	inADR := map[string]row{}
	for _, line := range strings.Split(string(raw), "\n") {
		if m := adrRow.FindStringSubmatch(line); m != nil {
			if _, dup := inADR[m[1]]; dup {
				t.Errorf("ADR 0012 lists %q twice", m[1])
			}
			inADR[m[1]] = row{class: m[2], status: m[3]}
		}
	}
	inCode := map[string]row{}
	for _, p := range inboundhttp.ProblemTypes() {
		inCode[p.Slug] = row{class: p.Class, status: fmt.Sprint(p.Status)}
	}
	for slug, c := range inCode {
		a, ok := inADR[slug]
		switch {
		case !ok:
			t.Errorf("problem type %q (%s %s) is in the catalogue but not in the ADR 0012 table", slug, c.class, c.status)
		case a != c:
			t.Errorf("problem type %q: the ADR says %s %s, the catalogue declares %s %s", slug, a.class, a.status, c.class, c.status)
		}
	}
	for slug := range inADR {
		if _, ok := inCode[slug]; !ok {
			t.Errorf("ADR 0012 lists %q, which is not a problem type of the catalogue", slug)
		}
	}
}

// A slug built twice (two titles) is one problem type: its entries share one class.
func TestEveryEntryOfOneSlugDeclaresTheSameClass(t *testing.T) {
	for _, p := range inboundhttp.ProblemTypes() {
		classes := inboundhttp.ClassesOfSlug(p.Slug)
		for _, c := range classes {
			if c != classes[0] {
				t.Errorf("problem type %q is declared with classes %v: one class per slug", p.Slug, classes)
				break
			}
		}
	}
}

// OUTCOME cannot exist in this store: a 4xx rolls the WHOLE unit of work back, the use
// cases' own units join it (postgres unit_of_work.go Do), so a refusal never commits
// state. Declaring one would store an answer that remembers writes that were discarded.
func TestNoProblemTypeIsAnOutcomeInThisStore(t *testing.T) {
	for _, p := range inboundhttp.ProblemTypes() {
		if p.Class == classOutcome {
			t.Errorf("problem type %q is OUTCOME, but idempotency.go serveFresh rolls every non-2xx answer back: nothing is committed to remember (ADR 0012)", p.Slug)
		}
	}
}

// The proof behind the previous test: a command that WROTE and then answered any problem
// type leaves no committed write and no key row for a RETRYABLE, and no committed write
// for a FINAL either (its answer is stored, its writes are not).
func TestARefusalNeverCommitsTheWritesOfItsCommand(t *testing.T) {
	for _, e := range newReplayAdapter(t).Catalogue() {
		if e.Status >= http.StatusInternalServerError {
			continue
		}
		t.Run(e.Slug, func(t *testing.T) {
			a := newReplayAdapter(t)
			h := &fakeHandler{script: []answerFn{func(w http.ResponseWriter, r *http.Request) {
				if err := a.Write(r.Context(), "command"); err != nil {
					t.Fatal(err)
				}
				a.Refuse(w, r, e)
			}}}
			mw := a.Middleware(t, h)
			post(t, mw, "k-"+e.Slug, `{"n":1}`)
			if got := a.Committed(t, "command"); got != 0 {
				t.Errorf("%s (%s): %d writes of the command committed with a refusal, want 0: the unit of work rolls back", e.Slug, e.Class, got)
			}
		})
	}
}

// writeError and writeProblem mark the capturing writer with the class each problem
// declares: this is how the middleware reads the class without a slug list.
func TestTheErrorWriterMarksTheClassOfEveryCatalogueEntry(t *testing.T) {
	for _, e := range inboundhttp.CatalogueErrors() {
		if got := inboundhttp.ClassMarkedByWriteError(e.Err); got != e.Class {
			t.Errorf("writeError(%v) marks %q, its catalogue entry %q declares %q", e.Err, got, e.Slug, e.Class)
		}
	}
	direct := 0
	inCatalogue := map[string]bool{}
	for _, e := range inboundhttp.CatalogueErrors() {
		inCatalogue[e.Slug] = true
	}
	for _, p := range inboundhttp.ProblemTypes() {
		if inCatalogue[p.Slug] {
			continue
		}
		direct++
		if got := inboundhttp.ClassMarkedByWriteProblem(p.Slug); got != p.Class {
			t.Errorf("writeProblem(%s) marks %q, the problem declares %q", p.Slug, got, p.Class)
		}
	}
	if direct == 0 {
		t.Fatal("no problem type is written directly: the registry is not read")
	}
}

// A problem is built by newProblem only (its class is a required argument and it lands in
// the registry the tests read): no composite literal of the problem type anywhere else in
// the package's production files.
func TestProblemsAreBuiltByTheConstructorOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			assertDeclBuildsNoProblemLiteral(t, f, decl)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d production files were read: the test scans nothing", checked)
	}
}

// assertDeclBuildsNoProblemLiteral fails when decl spells a problem literal instead of calling
// newProblem (the constructor is the only way a problem gets its class).
func assertDeclBuildsNoProblemLiteral(t *testing.T, f string, decl ast.Decl) {
	t.Helper()
	if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "newProblem" {
		return
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		if lit, ok := n.(*ast.CompositeLit); ok {
			if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "problem" {
				t.Errorf("%s builds a problem literal: use newProblem so it declares a class", f)
			}
		}
		return true
	})
}

// The middleware reads the class and holds no slug: no problem slug is spelt in the files
// that implement it.
func TestTheMiddlewareHoldsNoListOfSlugs(t *testing.T) {
	for _, f := range []string{"idempotency.go", "capture.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range inboundhttp.ProblemTypes() {
			if bytes.Contains(raw, []byte(`"`+p.Slug+`"`)) {
				t.Errorf("%s spells the slug %q: the class is read from the capture, not from a list", f, p.Slug)
			}
		}
	}
}

// A refusal that reaches the middleware with no class (written by hand, not through the
// error writer) is released like RETRYABLE: the safe fallback.
func TestARefusalWithNoClassIsReleased(t *testing.T) {
	a := newReplayAdapter(t)
	byHand := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"by-hand"}`))
	}
	h := &fakeHandler{script: []answerFn{byHand, succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	if first := post(t, mw, "k-by-hand", `{"n":1}`); first.Code != http.StatusConflict {
		t.Fatalf("first = %d, want 409", first.Code)
	}
	if got := a.KeyRows(t, "k-by-hand"); got != 0 {
		t.Errorf("key rows after an unclassified refusal = %d, want 0", got)
	}
	if retry := post(t, mw, "k-by-hand", `{"n":1}`); retry.Code != http.StatusCreated || h.calls != 2 {
		t.Errorf("retry = %d calls=%d, want a fresh 201 after 2 calls", retry.Code, h.calls)
	}
}

// A concurrent duplicate that WON stored its answer while this request ended RETRYABLE:
// the loser answers the stored answer (the key is no longer free) instead of its own
// refusal, and with another body it is the key-reuse conflict.
func TestARetryableLoserOfARaceAnswersWhatTheWinnerStored(t *testing.T) {
	store := memory.NewIdempotencyStore()
	uow := memory.NewUnitOfWork()
	const path, body = "/missed-load-sweeps", `{"n":1}`
	sum := sha256.Sum256([]byte("POST\n" + path + "\n" + body))
	winner := repository.IdempotencyRecord{
		Key: "k-race", Fingerprint: hex.EncodeToString(sum[:]), Status: http.StatusOK,
		ContentType: "application/json", Body: []byte(`{"body":"eyJ3b24iOnRydWV9"}`), // {"won":true}
		CreatedAt: replayClock{}.Now(),
	}
	loser := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The duplicate stored its 2xx while this request was still running.
		if err := store.Save(context.Background(), winner); err != nil {
			t.Error(err)
		}
		inboundhttp.Refuse(w, r, "concurrent-modification")
	})
	mw := inboundhttp.NewIdempotency(store, uow, replayClock{}).Middleware(loser)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, "k-race")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get(inboundhttp.IdempotencyReplayedHeader) != "true" || !strings.Contains(rec.Body.String(), "won") {
		t.Fatalf("the loser answered %d replayed=%q %q, want the winner's stored 200 replayed",
			rec.Code, rec.Header().Get(inboundhttp.IdempotencyReplayedHeader), rec.Body.String())
	}
}
