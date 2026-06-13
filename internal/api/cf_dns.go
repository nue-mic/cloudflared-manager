package api

import (
	"net/http"

	"github.com/mia-clark/cloudflared-manager/internal/cfapi"
)

// ZonesList lists the DNS zones visible to a stored account. An optional
// ?name= filters by exact domain.
func (h *CFHandler) ZonesList(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	zones, err := client.ListZones(reqCtx(r), r.URL.Query().Get("name"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": zones})
}

// DNSList lists records in a zone. ?name= filters by record host.
func (h *CFHandler) DNSList(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	recs, err := client.ListDNSRecords(reqCtx(r), cfParam(r, "zid"), r.URL.Query().Get("name"))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": recs})
}

// DNSCreate creates a record in a zone.
func (h *CFHandler) DNSCreate(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	var rec cfapi.DNSRecord
	if !decodeJSON(w, r, &rec) {
		return
	}
	if rec.Type == "" || rec.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "type and name are required", nil)
		return
	}
	out, err := client.CreateDNSRecord(reqCtx(r), cfParam(r, "zid"), rec)
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, out)
}

// DNSUpdate replaces a record.
func (h *CFHandler) DNSUpdate(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	var rec cfapi.DNSRecord
	if !decodeJSON(w, r, &rec) {
		return
	}
	out, err := client.UpdateDNSRecord(reqCtx(r), cfParam(r, "zid"), cfParam(r, "rid"), rec)
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, out)
}

// DNSDelete removes a record.
func (h *CFHandler) DNSDelete(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	if err := client.DeleteDNSRecord(reqCtx(r), cfParam(r, "zid"), cfParam(r, "rid")); err != nil {
		writeCFError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
