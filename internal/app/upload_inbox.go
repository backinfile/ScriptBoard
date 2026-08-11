package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"

	"scriptboard/internal/uploadinbox"
)

func (a *App) uploadInboxPage(response http.ResponseWriter, request *http.Request) {
	current := request.Context().Value(sessionContextKey).(session)
	entries, err := a.uploadInbox.List()
	if err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = uploadInboxTemplate.Execute(response, struct {
		Entries   []uploadinbox.Pending
		CSRFToken string
		Locale    webLocale
	}{Entries: entries, CSRFToken: current.csrfToken, Locale: resolveWebLocale(request)})
}

func (a *App) publishInboxUpload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	pending, payload, claim, err := a.uploadInbox.Claim(request.PathValue("id"))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer claim.Release()
	defer payload.Close()
	if subtle.ConstantTimeCompare([]byte(pending.SHA256), []byte(request.FormValue("sha256"))) != 1 {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusConflict)
		return
	}
	hash := sha256.New()
	size, err := io.Copy(hash, payload)
	if err != nil || size != pending.Size || subtle.ConstantTimeCompare([]byte(hex.EncodeToString(hash.Sum(nil))), []byte(pending.SHA256)) != 1 {
		http.Error(response, "staged upload integrity check failed", http.StatusConflict)
		return
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	name := pending.OriginalName
	publicationStarted := false
	if pending.ConflictPolicy == "rename" {
		name, err = a.files.AvailableName(pending.TargetDirectory, name)
	}
	if err == nil {
		err = claim.BeginPublication()
		publicationStarted = err == nil
	}
	if err == nil {
		_, err = a.files.Upload(pending.TargetDirectory, name, io.LimitReader(payload, pending.Size+1), pending.Size, false, "")
	}
	if err != nil {
		if publicationStarted && claim.CancelPublication() != nil {
			a.recordAuditForRequest(request, "publish_inbox_upload", pending.ID+" "+pending.SHA256, "failed_to_rollback")
		}
		http.Error(response, "could not publish staged upload", http.StatusConflict)
		return
	}
	if err := payload.Close(); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	if err := claim.Complete(); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "publish_inbox_upload", pending.ID+" "+pending.SHA256+" "+pending.TargetDirectory+"/"+name, "succeeded")
	http.Redirect(response, request, "/resources/inbox", http.StatusSeeOther)
}

func (a *App) discardInboxUpload(response http.ResponseWriter, request *http.Request) {
	if !validSessionCSRF(request) || request.FormValue("confirm") != "yes" {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusForbidden)
		return
	}
	pending, payload, claim, err := a.uploadInbox.Claim(request.PathValue("id"))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer claim.Release()
	defer payload.Close()
	if subtle.ConstantTimeCompare([]byte(pending.SHA256), []byte(request.FormValue("sha256"))) != 1 {
		http.Error(response, webText(resolveWebLocale(request), "error.forbidden"), http.StatusConflict)
		return
	}
	if err := payload.Close(); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	if err := claim.Complete(); err != nil {
		http.Error(response, webText(resolveWebLocale(request), "error.internal"), http.StatusInternalServerError)
		return
	}
	a.recordAuditForRequest(request, "discard_inbox_upload", pending.ID+" "+pending.SHA256, "succeeded")
	http.Redirect(response, request, "/resources/inbox", http.StatusSeeOther)
}
