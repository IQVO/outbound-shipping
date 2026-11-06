package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// handleStagePackage is POST /packages/{packageId}/stagings.
func (s *Server) handleStagePackage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "packageId")
	if id == "" {
		writeProblem(w, r, problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", "packageId is required"}, "")
		return
	}
	var req stagePackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.Stage.Handle(r.Context(), shared.PackageID(id), req.StagingLocation)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackageResponse(pkg))
}

// handleLoadPackage is POST /packages/{packageId}/loadings.
func (s *Server) handleLoadPackage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "packageId")
	var req loadPackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.Load.Handle(r.Context(), shared.PackageID(id), shared.LoadID(req.LoadID), shared.DoorCode(req.DoorCode))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackageResponse(pkg))
}

// handleUnloadPackage is POST /packages/{packageId}/unloadings.
func (s *Server) handleUnloadPackage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "packageId")
	var req unloadPackageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	pkg, err := s.Unload.Handle(r.Context(), shared.PackageID(id), req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPackageResponse(pkg))
}

// handleCloseLoad is POST /loads/{loadId}/closures.
func (s *Server) handleCloseLoad(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "loadId")
	var req closeLoadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	shorts := make([]shared.ShortShip, 0, len(req.ShortShips))
	for _, ss := range req.ShortShips {
		shorts = append(shorts, shared.ShortShip{PackageID: shared.PackageID(ss.PackageID), Reason: ss.Reason})
	}
	load, err := s.Close.Handle(r.Context(), shared.LoadID(id), shorts)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLoadResponse(load))
}

// handleReopenLoad is POST /loads/{loadId}/reopenings.
func (s *Server) handleReopenLoad(w http.ResponseWriter, r *http.Request) {
	load, err := s.Reopen.Handle(r.Context(), shared.LoadID(chi.URLParam(r, "loadId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLoadResponse(load))
}

// handleCancelLoad is POST /loads/{loadId}/cancellations.
func (s *Server) handleCancelLoad(w http.ResponseWriter, r *http.Request) {
	var req cancelLoadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	load, err := s.Cancel.Handle(r.Context(), shared.LoadID(chi.URLParam(r, "loadId")), req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLoadResponse(load))
}

// handleVoidBol is POST /bills-of-lading/{bolNumber}/voids.
func (s *Server) handleVoidBol(w http.ResponseWriter, r *http.Request) {
	var req voidBolRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	bol, err := s.VoidBol.Handle(r.Context(), shared.BolNumber(chi.URLParam(r, "bolNumber")), req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBolResponse(bol))
}

// handleMissedLoadSweep is POST /missed-load-sweeps.
func (s *Server) handleMissedLoadSweep(w http.ResponseWriter, r *http.Request) {
	var req missedLoadSweepRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.SiteID == "" {
		writeProblem(w, r, problem{http.StatusBadRequest, "bad-request", "Malformed request", "site_id is required"}, "")
		return
	}
	var cutoff time.Time
	if req.Cutoff != "" {
		t, err := time.Parse(time.RFC3339, req.Cutoff)
		if err != nil {
			writeProblem(w, r, problem{http.StatusBadRequest, "bad-request", "Malformed request", "cutoff must be RFC 3339"}, "")
			return
		}
		cutoff = t
	}
	flagged, sweptAt, err := s.Sweep.Handle(r.Context(), shared.SiteID(req.SiteID), cutoff)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sweepResponse{Flagged: flagged, SweptAt: sweptAt.UTC().Format(time.RFC3339)})
}

// handleGetLoad is GET /loads/{loadId}.
func (s *Server) handleGetLoad(w http.ResponseWriter, r *http.Request) {
	load, err := s.Queries.GetLoad(r.Context(), shared.LoadID(chi.URLParam(r, "loadId")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLoadResponse(load))
}

// handleListLoads is GET /loads.
func (s *Server) handleListLoads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for name := range q {
		switch name {
		case "site_id", "state", "operating_date", "limit", "cursor":
		default:
			writeError(w, r, errUnknownQueryParameter)
			return
		}
	}
	state := q.Get("state")
	if state != "" && !validLoadState(state) {
		writeProblem(w, r, problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", "state must be a load state"}, "")
		return
	}
	date := outboundload.OperatingDate(q.Get("operating_date"))
	if date != "" && !date.Valid() {
		writeProblem(w, r, problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", "operating_date must be YYYY-MM-DD"}, "")
		return
	}
	limit, err := usecases.ParseLimit(q.Get("limit"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, next, err := s.Queries.ListLoads(r.Context(), repository.LoadFilter{
		Site:          shared.SiteID(q.Get("site_id")),
		State:         outboundload.State(state),
		OperatingDate: date,
	}, q.Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]loadResponse, 0, len(page))
	for _, l := range page {
		items = append(items, toLoadResponse(l))
	}
	writeJSON(w, http.StatusOK, loadPage{Items: nonNil(items), NextCursor: cursorOf(next)})
}

// handleListLoadPackages is GET /loads/{loadId}/packages.
func (s *Server) handleListLoadPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for name := range q {
		switch name {
		case "state", "limit", "cursor":
		default:
			writeError(w, r, errUnknownQueryParameter)
			return
		}
	}
	state := q.Get("state")
	if state != "" && !validPackageState(state) {
		writeProblem(w, r, problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", "state must be a package state"}, "")
		return
	}
	limit, err := usecases.ParseLimit(q.Get("limit"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, next, err := s.Queries.ListLoadPackages(r.Context(),
		shared.LoadID(chi.URLParam(r, "loadId")),
		repository.PackageFilter{State: state}, q.Get("cursor"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]packageResponse, 0, len(page))
	for _, p := range page {
		items = append(items, toPackageResponse(p))
	}
	writeJSON(w, http.StatusOK, packagePage{Items: nonNilPackages(items), NextCursor: cursorOf(next)})
}

// handleGetBol is GET /bills-of-lading/{bolNumber}.
func (s *Server) handleGetBol(w http.ResponseWriter, r *http.Request) {
	bol, err := s.Queries.GetBillOfLading(r.Context(), shared.BolNumber(chi.URLParam(r, "bolNumber")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBolResponse(bol))
}

// handleLoadAdherence is GET /reports/load-adherence.
func (s *Server) handleLoadAdherence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for name := range q {
		switch name {
		case "site_id", "operating_date":
		default:
			writeError(w, r, errUnknownQueryParameter)
			return
		}
	}
	date := outboundload.OperatingDate(q.Get("operating_date"))
	if date != "" && !date.Valid() {
		writeProblem(w, r, problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", "operating_date must be YYYY-MM-DD"}, "")
		return
	}
	rows, generatedAt, err := s.Queries.AdherenceReport(r.Context(), shared.SiteID(q.Get("site_id")), date)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]adherenceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, adherenceRow{
			SiteID: string(row.Site), CptID: string(row.Cpt),
			OperatingDate: string(row.OperatingDate),
			LoadsClosed:   row.LoadsClosed, LoadsTotal: row.LoadsTotal,
			PackagesMissed: row.PackagesMissed, Adherence: row.Adherence,
		})
	}
	writeJSON(w, http.StatusOK, adherenceReport{Rows: out, GeneratedAt: generatedAt.UTC().Format(time.RFC3339)})
}

func validLoadState(s string) bool {
	switch outboundload.State(s) {
	case outboundload.StatePlanned, outboundload.StateLoading,
		outboundload.StateClosed, outboundload.StateShipConfirmed,
		outboundload.StateCancelled:
		return true
	}
	return false
}

func validPackageState(s string) bool {
	switch s {
	case "EXPECTED", "STAGED", "LOADED", "SHIPPED", "SHORT_SHIPPED":
		return true
	}
	return false
}
