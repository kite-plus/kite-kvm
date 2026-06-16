package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
	"github.com/kite-plus/kite-kvm/internal/vm"
)

type vmsHandler struct {
	service *vm.Service
}

func (h *vmsHandler) list(w http.ResponseWriter, r *http.Request) {
	vms, err := h.service.List(r.Context())
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	if vms == nil {
		vms = []*model.VM{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": vms})
}

func (h *vmsHandler) get(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *vmsHandler) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.service.Status(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *vmsHandler) stats(w http.ResponseWriter, r *http.Request) {
	info, err := h.service.Stats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *vmsHandler) password(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.PasswordReset(r.Context(), chi.URLParam(r, "id"), req.Password)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) hostname(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.SetHostname(r.Context(), chi.URLParam(r, "id"), req.Hostname)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) resize(w http.ResponseWriter, r *http.Request) {
	var req vm.ResizeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.Resize(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) traffic(w http.ResponseWriter, r *http.Request) {
	info, err := h.service.TrafficUsage(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *vmsHandler) setTrafficQuota(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuotaGB int `json:"quota_gb"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	info, err := h.service.SetTrafficQuota(r.Context(), chi.URLParam(r, "id"), req.QuotaGB)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *vmsHandler) listSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := h.service.SnapshotList(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	if snaps == nil {
		snaps = []libvirt.SnapshotInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (h *vmsHandler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.SnapshotCreate(r.Context(), chi.URLParam(r, "id"), req.Name, req.Description)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	j, err := h.service.SnapshotDelete(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "snap"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) revertSnapshot(w http.ResponseWriter, r *http.Request) {
	j, err := h.service.SnapshotRevert(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "snap"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) rebuild(w http.ResponseWriter, r *http.Request) {
	var req vm.RebuildRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	// An empty body is valid: reinstall from the current image, keeping creds.
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.Rebuild(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) terminate(w http.ResponseWriter, r *http.Request) {
	j, err := h.service.Terminate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

func (h *vmsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req vm.CreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIdempotentBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, errBadRequest("invalid JSON body"))
		return
	}
	j, err := h.service.Create(r.Context(), req)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, acceptedJob(j))
}

// vmOp is a service method that enqueues a job for a VM by id.
type vmOp func(ctx context.Context, id string) (*model.Job, error)

// powerOp builds a handler that enqueues the given operation and returns 202.
func (h *vmsHandler) powerOp(op vmOp) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, err := op(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, mapVMError(err))
			return
		}
		w.Header().Set("Location", "/v1/jobs/"+j.ID)
		writeJSON(w, http.StatusAccepted, acceptedJob(j))
	}
}

// acceptedJob is the standard 202 body for an enqueued mutating operation.
func acceptedJob(j *model.Job) map[string]any {
	return map[string]any{
		"job_id": j.ID,
		"status": j.State,
		"vm_id":  j.VMID,
	}
}

// mapVMError maps service errors to API errors.
func mapVMError(err error) error {
	switch {
	case errors.Is(err, vm.ErrFlavorNotFound),
		errors.Is(err, vm.ErrImageNotFound),
		errors.Is(err, vm.ErrNetworkNotFound):
		return errUnprocessable(err.Error())
	case errors.Is(err, vm.ErrInvalidRequest):
		return errBadRequest(err.Error())
	case errors.Is(err, vm.ErrVMNotFound):
		return errNotFound(err.Error())
	case errors.Is(err, vm.ErrVMTerminated):
		return errConflict(err.Error())
	case errors.Is(err, vm.ErrVMNotRunning):
		return errConflict(err.Error())
	case errors.Is(err, store.ErrNoIPAvailable):
		return errConflict(err.Error())
	default:
		return errInternal("internal server error")
	}
}
