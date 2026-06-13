package api

import (
	"context"
	"net/http"

	"github.com/mia-clark/cloudflared-manager/internal/cfaccount"
	"github.com/mia-clark/cloudflared-manager/internal/cfapi"
)

// accountReq is the body for creating/updating a Cloudflare account. Empty
// secret fields on update mean "keep the existing value".
type accountReq struct {
	Name      string `json:"name"`
	AuthType  string `json:"auth_type"`
	Token     string `json:"token"`
	Email     string `json:"email"`
	APIKey    string `json:"api_key"`
	AccountID string `json:"account_id"`
}

// verifyResult is the verification summary attached to create/update/verify
// responses so the UI can react (e.g. prompt to pick an account when several
// are visible).
type verifyResult struct {
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Accounts []cfapi.Account `json:"accounts,omitempty"`
}

// AccountsList returns redacted views of every stored account.
func (h *CFHandler) AccountsList(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"items": h.store.List()})
}

// AccountsGet returns one account's redacted view.
func (h *CFHandler) AccountsGet(w http.ResponseWriter, r *http.Request) {
	v, ok := h.store.Get(cfParam(r, "aid"))
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "cf account not found", nil)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

// AccountsCreate stores a new account then verifies it, auto-discovering the
// Cloudflare account id when unambiguous.
func (h *CFHandler) AccountsCreate(w http.ResponseWriter, r *http.Request) {
	var req accountReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AuthType == "" {
		req.AuthType = cfaccount.AuthToken
	}
	view, err := h.store.Create(cfaccount.CreateInput{
		Name:      req.Name,
		AuthType:  req.AuthType,
		Token:     req.Token,
		Email:     req.Email,
		Key:       req.APIKey,
		AccountID: req.AccountID,
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}
	view, vr := h.runVerify(reqCtx(r), view.ID)
	WriteJSON(w, http.StatusCreated, map[string]any{"account": view, "verify": vr})
}

// AccountsUpdate applies partial changes then re-verifies.
func (h *CFHandler) AccountsUpdate(w http.ResponseWriter, r *http.Request) {
	aid := cfParam(r, "aid")
	var req accountReq
	if !decodeJSON(w, r, &req) {
		return
	}
	view, err := h.store.Update(aid, cfaccount.UpdateInput{
		Name:      req.Name,
		AuthType:  req.AuthType,
		Token:     req.Token,
		Email:     req.Email,
		Key:       req.APIKey,
		AccountID: req.AccountID,
	})
	if err != nil {
		if err == cfaccount.ErrNotFound {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error(), nil)
			return
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}
	view, vr := h.runVerify(reqCtx(r), view.ID)
	WriteJSON(w, http.StatusOK, map[string]any{"account": view, "verify": vr})
}

// AccountsDelete removes an account.
func (h *CFHandler) AccountsDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Delete(cfParam(r, "aid")); err != nil {
		if err == cfaccount.ErrNotFound {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error(), nil)
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeInternal, err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AccountsVerify re-checks an account's credentials.
func (h *CFHandler) AccountsVerify(w http.ResponseWriter, r *http.Request) {
	aid := cfParam(r, "aid")
	if _, ok := h.store.Get(aid); !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "cf account not found", nil)
		return
	}
	view, vr := h.runVerify(reqCtx(r), aid)
	WriteJSON(w, http.StatusOK, map[string]any{"account": view, "verify": vr})
}

// AccountsCFList lists the Cloudflare accounts visible to a stored credential
// (used by the UI to pick an account_id when several exist).
func (h *CFHandler) AccountsCFList(w http.ResponseWriter, r *http.Request) {
	client, _, ok := h.clientFor(w, cfParam(r, "aid"))
	if !ok {
		return
	}
	accounts, err := client.ListAccounts(reqCtx(r))
	if err != nil {
		writeCFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": accounts})
}

// runVerify checks the stored credential, resolves the Cloudflare account id
// when unambiguous, persists the outcome and returns the fresh view + summary.
func (h *CFHandler) runVerify(ctx context.Context, localID string) (cfaccount.View, verifyResult) {
	view, ok := h.store.Get(localID)
	if !ok {
		return cfaccount.View{}, verifyResult{OK: false, Error: "account not found"}
	}
	sec, err := h.store.Secret(localID)
	if err != nil {
		return view, verifyResult{OK: false, Error: err.Error()}
	}
	client := h.newClient(sec)

	var email string
	if sec.AuthType == cfaccount.AuthToken {
		if _, verr := client.VerifyToken(ctx); verr != nil {
			_ = h.store.SetVerification(localID, "", "", "", cfaccount.StatusInvalid)
			v, _ := h.store.Get(localID)
			return v, verifyResult{OK: false, Error: verr.Error()}
		}
	} else {
		u, uerr := client.GetUser(ctx)
		if uerr != nil {
			_ = h.store.SetVerification(localID, "", "", "", cfaccount.StatusInvalid)
			v, _ := h.store.Get(localID)
			return v, verifyResult{OK: false, Error: uerr.Error()}
		}
		email = u.Email
	}

	accounts, aerr := client.ListAccounts(ctx)
	if aerr != nil {
		_ = h.store.SetVerification(localID, "", "", email, cfaccount.StatusInvalid)
		v, _ := h.store.Get(localID)
		return v, verifyResult{OK: false, Error: aerr.Error()}
	}

	// Resolve the account id: keep an explicit/previous choice, otherwise pick
	// the only account when there is exactly one.
	cfID, cfName := view.AccountID, ""
	for _, a := range accounts {
		if a.ID == cfID {
			cfName = a.Name
		}
	}
	if cfID == "" && len(accounts) == 1 {
		cfID, cfName = accounts[0].ID, accounts[0].Name
	}
	_ = h.store.SetVerification(localID, cfID, cfName, email, cfaccount.StatusActive)
	v, _ := h.store.Get(localID)
	return v, verifyResult{OK: true, Accounts: accounts}
}
