package http

import (
	"net/http"
	"net/http/httptest"
)

// ProblemType is one problem type of the service as the replay-class tests read it:
// the production catalogue, one value per slug.
type ProblemType struct {
	Slug   string
	Status int
	Class  string // "" when the problem declares none
}

// ProblemTypes returns every problem type this service can answer, once per slug: the
// catalogue entries and the problems the writers build directly. Both come from the
// registry newProblem fills, so a problem that is not built there is not a problem.
func ProblemTypes() []ProblemType {
	var out []ProblemType
	seen := map[string]bool{}
	for _, p := range registry {
		if !seen[p.slug] {
			seen[p.slug] = true
			out = append(out, ProblemType{Slug: p.slug, Status: p.status, Class: string(p.class)})
		}
	}
	return out
}

// ClassesOfSlug returns the class of every registered problem with this slug (a slug
// built twice, with two titles, must declare one class).
func ClassesOfSlug(slug string) []string {
	var out []string
	for _, p := range registry {
		if p.slug == slug {
			out = append(out, string(p.class))
		}
	}
	return out
}

// CatalogueErrors returns every typed error of the catalogue with the slug and class its
// entry declares.
func CatalogueErrors() []CatalogueError {
	out := make([]CatalogueError, 0, len(problemCatalogue))
	for _, e := range problemCatalogue {
		out = append(out, CatalogueError{Err: e.err, Slug: e.p.slug, Status: e.p.status, Class: string(e.p.class)})
	}
	return out
}

// CatalogueError is one row of problemCatalogue.
type CatalogueError struct {
	Err    error
	Slug   string
	Status int
	Class  string
}

// Refuse answers the problem type slug exactly as production does: through writeError
// for a typed error of the catalogue, through writeProblem for a problem a writer builds
// directly. Both mark the capturing writer with the class.
func Refuse(w http.ResponseWriter, r *http.Request, slug string) {
	for _, e := range problemCatalogue {
		if e.p.slug == slug {
			writeError(w, r, e.err)
			return
		}
	}
	for _, p := range registry {
		if p.slug == slug {
			writeProblem(w, r, p, "refused")
			return
		}
	}
	panic("no problem type " + slug)
}

// ClassMarkedByWriteError reports the class the production error writer marks on the
// capturing writer for err ("" when it marks none).
func ClassMarkedByWriteError(err error) string {
	out := &capture{header: http.Header{}}
	writeError(out, httptest.NewRequest(http.MethodPost, "/", nil), err)
	return string(out.class)
}

// ClassMarkedByWriteProblem is the same for a problem the writers build directly.
func ClassMarkedByWriteProblem(slug string) string {
	out := &capture{header: http.Header{}}
	for _, p := range registry {
		if p.slug == slug {
			writeProblem(out, httptest.NewRequest(http.MethodPost, "/", nil), p, "")
		}
	}
	return string(out.class)
}
