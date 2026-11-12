package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const (
	passkeysPath          = twoFactorEnrolPath + "/passkeys"
	passkeyLoginBeginPath = SecondFactorPath + "/passkey/begin"
	passkeyLoginPath      = SecondFactorPath + "/passkey/finish"
)

func (h *handlers) registerPasskeys() {
	r := h.deps.Router.Admin
	r.With(RequireRecentAuth).Post("/account/two-factor/passkeys/register/begin", h.passkeyRegisterBegin)
	r.With(RequireRecentAuth).Post("/account/two-factor/passkeys/register/finish", h.passkeyRegisterFinish)
	r.Post("/account/two-factor/passkeys/{id}/remove", h.passkeyRemove) // asks for the password itself
	r.Post("/login/2fa/passkey/begin", h.passkeyLoginBegin)
	r.Post("/login/2fa/passkey/finish", h.passkeyLoginFinish)
}

// passkeyRP returns the Relying Party for this instance, or nil when the
// base URL cannot host WebAuthn.
func (h *handlers) passkeyRP(ctx context.Context) *webauthnRP {
	name := ""
	if h.deps.Settings != nil {
		name = h.deps.Settings.GetString(ctx, "site.title", "")
	}
	rp, err := newWebAuthnRP(h.deps.Config.BaseURL, name)
	if err != nil {
		return nil
	}
	return rp
}

// passkeyFail writes a JSON error with a translated message.
func (h *handlers) passkeyFail(w http.ResponseWriter, r *http.Request, status int, code, key string) {
	msg := key
	if h.deps.I18n != nil {
		msg = h.deps.I18n.T(i18n.Locale(r.Context()), key)
	}
	httpx.Error(w, status, code, msg, nil)
}

// passkeyCeremonyRP checks that passkeys are available and that the request
// comes from the configured origin (the browser's Origin header; the
// library checks clientDataJSON as well). It writes the error itself.
func (h *handlers) passkeyCeremonyRP(w http.ResponseWriter, r *http.Request) *webauthnRP {
	rp := h.passkeyRP(r.Context())
	if rp == nil {
		h.passkeyFail(w, r, http.StatusConflict, "passkeys_unavailable", "passkeys.error.unavailable")
		return nil
	}
	if !strings.EqualFold(r.Header.Get("Origin"), rp.origin) {
		h.passkeyFail(w, r, http.StatusForbidden, "origin_mismatch", "passkeys.error.origin")
		return nil
	}
	return rp
}

// readCeremonyBody reads a JSON body of at most maxPasskeyBody bytes.
func readCeremonyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
	return b, err == nil && len(b) > 0
}

// passkeyRegisterBegin returns creation options for a new passkey of the
// signed-in user (recent sign-in or password confirmation required).
func (h *handlers) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	rp := h.passkeyCeremonyRP(w, r)
	if rp == nil {
		return
	}
	ctx := r.Context()
	wu, err := h.passkeys.webauthnUser(ctx, u, rp.rpID, true)
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	creation, sd, err := rp.wa.BeginRegistration(wu, webauthn.WithExclusions(webauthn.Credentials(wu.creds).CredentialDescriptors()))
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	if err := h.passkeys.saveCeremony(ctx, ceremonyRegister, u.ID, hashToken(principalFrom(ctx).token), sd); err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, creation, nil)
}

// passkeyRegisterFinish verifies the authenticator's answer and stores the
// passkey. When it is the user's first second factor and they hold no
// recovery codes, ten new codes are returned once.
func (h *handlers) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	rp := h.passkeyCeremonyRP(w, r)
	if rp == nil {
		return
	}
	ctx := r.Context()
	body, ok := readCeremonyBody(w, r)
	var in struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if !ok || json.Unmarshal(body, &in) != nil || len(in.Credential) == 0 {
		h.passkeyFail(w, r, http.StatusBadRequest, "invalid_request", "passkeys.error.invalid")
		return
	}
	name, ok := cleanPasskeyName(in.Name)
	if !ok {
		h.passkeyFail(w, r, http.StatusUnprocessableEntity, "invalid_name", "passkeys.error.name")
		return
	}
	if name == "" {
		name = "Passkey"
		if h.deps.I18n != nil {
			name = h.deps.I18n.T(i18n.Locale(ctx), "passkeys.default_name")
		}
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		h.passkeyFail(w, r, http.StatusBadRequest, "invalid_credential", "passkeys.error.invalid")
		return
	}
	owner, sd, err := h.passkeys.takeCeremony(ctx, ceremonyRegister, hashToken(principalFrom(ctx).token), parsed.Response.CollectedClientData.Challenge)
	if err != nil || owner != u.ID {
		if err != nil && !errors.Is(err, errCeremonyNotFound) {
			h.passkeyServerError(w, r, err)
			return
		}
		h.passkeyFail(w, r, http.StatusBadRequest, "ceremony_expired", "passkeys.error.expired")
		return
	}
	wu, err := h.passkeys.webauthnUser(ctx, u, rp.rpID, false)
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	cred, err := rp.wa.CreateCredential(wu, *sd, parsed)
	if err != nil {
		h.deps.Log.Info("auth: passkey registration refused", "user_id", u.ID, "reason", protocolReason(err))
		h.passkeyFail(w, r, http.StatusBadRequest, "invalid_credential", "passkeys.error.invalid")
		return
	}
	id, err := h.passkeys.add(ctx, u.ID, rp.rpID, name, cred)
	if errors.Is(err, errPasskeyExists) {
		h.passkeyFail(w, r, http.StatusConflict, "passkey_exists", "passkeys.error.exists")
		return
	}
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	h.deps.Audit.Record(ctx, "user.passkey_added", "user", u.ID, map[string]string{"passkey_id": id, "name": name})
	out := map[string]any{"id": id}
	// A user without unused recovery codes (first factor, or all used up)
	// gets a set now, shown once. Decided after storing the passkey, so a
	// parallel removal of another factor cannot leave a passkey without codes.
	codes, err := h.twoFactor.issueFirstRecoveryCodes(ctx, u.ID)
	if err != nil {
		// The passkey is stored; the next one added, or the renewal form,
		// can still provide codes.
		h.deps.Log.Error("auth: issue recovery codes", "err", err)
	} else if codes != nil {
		out["codes"] = codes
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, out, nil)
}

// passkeyRemove deletes one of the user's passkeys after the current
// password, entered in the form (rate-limited like sign-in; accounts without
// a password need a recent sign-in instead). Removing a factor weakens the
// sign-in, so a session alone is not enough, as for turning off the app. The
// last usable factor of a role that requires two-factor authentication
// cannot be removed; removing the last factor also deletes the recovery codes.
func (h *handlers) passkeyRemove(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r)
	if u == nil {
		deny(w, r, http.StatusForbidden, "forbidden", "a signed-in session is required")
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	problem, status, err := h.confirmPassword(r, u, r.PostFormValue("password"))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if problem != "" {
		h.twoFactorRender(w, r, status, u, twoFactorPage{Error: problem})
		return
	}
	rpID := ""
	if rp := h.passkeyRP(ctx); rp != nil {
		rpID = rp.rpID
	}
	required := slices.Contains(TwoFactorRequiredRoles(ctx, h.deps), u.Role)
	name, factorsLeft, err := h.passkeys.remove(ctx, u.ID, id, rpID, required)
	switch {
	case errors.Is(err, errLastRequiredFactor):
		h.twoFactorRender(w, r, http.StatusForbidden, u, twoFactorPage{Error: "passkeys.remove.required"})
		return
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, passkeysPath, http.StatusSeeOther)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	meta := map[string]any{"passkey_id": id, "name": name}
	if !factorsLeft {
		meta["two_factor_off"] = true
	}
	h.deps.Audit.Record(ctx, "user.passkey_removed", "user", u.ID, meta)
	httpx.SetFlash(w, "success", "passkeys.remove.done")
	http.Redirect(w, r, passkeysPath, http.StatusSeeOther)
}

// passkeyLoginBegin returns request options for the pending sign-in's user.
func (h *handlers) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	ch, err := h.loadChallenge(r)
	if err != nil {
		if !errors.Is(err, ErrInvalidToken) {
			h.passkeyServerError(w, r, err)
			return
		}
		h.passkeyFail(w, r, http.StatusBadRequest, "sign_in_expired", "twofactor.login.expired")
		return
	}
	rp := h.passkeyCeremonyRP(w, r)
	if rp == nil {
		return
	}
	ctx := r.Context()
	u, err := h.svc.Get(ctx, ch.userID)
	if err != nil || u.Status != StatusActive {
		h.passkeyFail(w, r, http.StatusBadRequest, "sign_in_expired", "twofactor.login.expired")
		return
	}
	wu, err := h.passkeys.webauthnUser(ctx, u, rp.rpID, false)
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	if len(wu.creds) == 0 || len(wu.handle) == 0 {
		h.passkeyFail(w, r, http.StatusConflict, "no_passkeys", "passkeys.error.none")
		return
	}
	assertion, sd, err := rp.wa.BeginLogin(wu, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	if err := h.passkeys.saveCeremony(ctx, ceremonyLogin, u.ID, ch.hash, sd); err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, assertion, nil)
}

// passkeyLoginFinish verifies the assertion under the second-factor rate
// limit and turns the pending sign-in into a session.
func (h *handlers) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	ch, err := h.loadChallenge(r)
	if err != nil {
		if !errors.Is(err, ErrInvalidToken) {
			h.passkeyServerError(w, r, err)
			return
		}
		h.clearChallengeCookie(w, r)
		h.passkeyFail(w, r, http.StatusBadRequest, "sign_in_expired", "twofactor.login.expired")
		return
	}
	ctx := r.Context()
	fail := func(status int, reason, code, key string) {
		h.failChallenge(ctx, ch)
		h.deps.Audit.Record(ctx, "user.passkey_failed", "user", ch.userID, map[string]string{"reason": reason})
		h.passkeyFail(w, r, status, code, key)
	}
	rp := h.passkeyRP(ctx)
	if rp == nil {
		h.passkeyFail(w, r, http.StatusConflict, "passkeys_unavailable", "passkeys.error.unavailable")
		return
	}
	if !strings.EqualFold(r.Header.Get("Origin"), rp.origin) {
		// Audited, but it does not use up the pending sign-in's attempts: a
		// foreign page must not be able to end someone's sign-in.
		h.deps.Audit.Record(ctx, "user.passkey_failed", "user", ch.userID, map[string]string{"reason": "origin"})
		h.passkeyFail(w, r, http.StatusForbidden, "origin_mismatch", "passkeys.error.origin")
		return
	}
	if locked, err := h.secondFactorLimited(ctx, ch.userID); err != nil {
		h.passkeyServerError(w, r, err)
		return
	} else if locked {
		fail(http.StatusTooManyRequests, "locked", "locked", "auth.login.locked")
		return
	}
	body, ok := readCeremonyBody(w, r)
	if !ok {
		fail(http.StatusBadRequest, "invalid", "invalid_request", "passkeys.error.invalid")
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		fail(http.StatusUnauthorized, "invalid", "invalid_credential", "passkeys.error.invalid")
		return
	}
	owner, sd, err := h.passkeys.takeCeremony(ctx, ceremonyLogin, ch.hash, parsed.Response.CollectedClientData.Challenge)
	if err != nil {
		if !errors.Is(err, errCeremonyNotFound) {
			h.passkeyServerError(w, r, err)
			return
		}
		fail(http.StatusUnauthorized, "expired", "ceremony_expired", "passkeys.error.expired")
		return
	}
	u, err := h.svc.Get(ctx, ch.userID)
	if err != nil || u.Status != StatusActive || owner != u.ID {
		h.consumeChallenge(ctx, ch)
		h.clearChallengeCookie(w, r)
		h.passkeyFail(w, r, http.StatusBadRequest, "sign_in_expired", "twofactor.login.expired")
		return
	}
	actx := WithUser(ctx, u) // audit entries name the user
	wu, err := h.passkeys.webauthnUser(ctx, u, rp.rpID, false)
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	cred, err := rp.wa.ValidateLogin(wu, *sd, parsed)
	if err != nil {
		h.deps.Log.Info("auth: passkey sign-in refused", "user_id", u.ID, "reason", protocolReason(err))
		h.failChallenge(ctx, ch)
		h.deps.Audit.Record(actx, "user.passkey_failed", "user", u.ID, map[string]string{"reason": "invalid"})
		h.passkeyFail(w, r, http.StatusUnauthorized, "invalid_credential", "passkeys.error.invalid")
		return
	}
	passkeyID := ""
	if cred.Authenticator.CloneWarning {
		err = errSignCountRegressed
	} else {
		passkeyID, err = h.passkeys.recordUse(ctx, u.ID, cred)
	}
	if errors.Is(err, errSignCountRegressed) {
		// WebAuthn L3 §6.1.1: a counter that does not grow may mean a cloned
		// authenticator. Refuse and keep the stored counter.
		h.failChallenge(ctx, ch)
		h.deps.Audit.Record(actx, "user.passkey_failed", "user", u.ID, map[string]string{"reason": "sign_count"})
		h.passkeyFail(w, r, http.StatusUnauthorized, "sign_count", "passkeys.error.sign_count")
		return
	}
	if err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	if !h.consumeChallenge(ctx, ch) {
		h.clearChallengeCookie(w, r)
		h.passkeyFail(w, r, http.StatusBadRequest, "sign_in_expired", "twofactor.login.expired")
		return
	}
	_ = h.svc.limiter.reset(ctx, secondFactorLimitKey(u.ID))
	_, _ = h.deps.DB.ExecContext(ctx, `DELETE FROM webauthn_challenges WHERE binding = ?`, ch.hash)
	h.clearChallengeCookie(w, r)
	if err := h.startSession(w, r, u, ch.method); err != nil {
		h.passkeyServerError(w, r, err)
		return
	}
	h.deps.Audit.Record(actx, "user.passkey_used", "user", u.ID, map[string]string{"passkey_id": passkeyID})
	next := ch.next
	if u.onboardingRequired {
		next = onboardingPath
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"redirect": next}, nil)
}

func (h *handlers) passkeyServerError(w http.ResponseWriter, r *http.Request, err error) {
	h.deps.Log.Error("auth: passkey", "err", err, "path", httpx.RedactPath(r.URL.Path), "request_id", httpx.RequestIDFromContext(r.Context()))
	h.passkeyFail(w, r, http.StatusInternalServerError, "server_error", "passkeys.error.server")
}

// protocolReason is the library's short error description for the log
// (never request bytes).
func protocolReason(err error) string {
	var perr *protocol.Error
	if errors.As(err, &perr) {
		return perr.Type + ": " + perr.Details
	}
	return err.Error()
}
