package admin

import (
	"github.com/zadstack/gorun/internal/auth"
)

// routes mounts the admin screens on Router.Admin (prefix /admin), each
// behind its permission. Routes are registered inline (no sub-router
// mounts) so other modules may add their own /settings/* or /system/*
// pages.
func (h *handlers) routes() {
	h.pageAssistantRoutes()
	h.errorInventoryRoutes()
	h.metricoolRoutes()
	h.canvasRoutes()
	h.dashboardPinRoutes()
	h.dashboardLayoutRoutes()
	r := h.d.Router.Admin
	r.Get("/", h.dashboard)
	r.Get("/search", h.searchPage)
	r.Get("/x/search", h.searchFragment)
	r.Post("/x/ai/write", h.aiWrite) // content.edit or content.create, checked inside (JSON 403)

	users := r.With(auth.RequirePerm(PermUsers))
	users.Get("/users", h.usersList)
	users.Get("/users/new", h.userNew)
	users.Post("/users", h.userCreate)
	users.Get("/users/{id}", h.userEdit)
	users.Post("/users/{id}", h.userUpdate)
	users.Post("/users/{id}/delete", h.userDelete)
	users.Post("/users/{id}/magic-link", h.userMagicLink)

	st := r.With(auth.RequirePerm(PermSettings))
	st.Get("/settings/general", h.settingsGeneral)
	st.Post("/settings/general", h.settingsGeneralSave)
	st.Get("/x/settings/front-page", h.frontPagePicker)
	st.Get("/settings/mail", h.settingsMail)
	st.Post("/settings/mail", h.settingsMailSave)
	st.Post("/settings/mail/test", h.settingsMailTest)
	st.Get("/settings/ai", h.settingsAI)
	st.Post("/settings/ai", h.settingsAISave)
	st.Get("/settings/storage", h.storageSettings)
	st.Post("/settings/storage/activate", h.storageActivate)
	st.Post("/settings/storage/disable", h.storageDisable)
	st.Get("/settings/connections", h.connectionsList)
	st.Post("/settings/connections", h.connectionsSave)
	st.Post("/settings/connections/models", h.connectionsModels)
	st.Post("/settings/connections/{id}/test", h.connectionsTest)
	st.Post("/settings/connections/{id}/remove", h.connectionsRemove)
	st.Post("/settings/connections/{id}/use-ai", h.connectionsUseAI)

	sys := r.With(auth.RequirePerm(PermHealth))
	sys.Get("/system/health", h.health)
	sys.Get("/system/jobs", h.jobsList)
	sys.Post("/system/jobs/{id}/retry", h.jobRetry)
	sys.Get("/agents/audit", h.auditList)

	bk := r.With(auth.RequirePerm(PermBackup))
	bk.Get("/system/backups", h.backupsList)
	bk.Post("/system/backups", h.backupCreate)
	bk.Post("/system/backups/schedule", h.backupScheduleSave)
	bk.Post("/system/backups/remote", h.backupRemoteCreate)
	bk.Get("/system/backups/{stamp}/inspect", h.backupInspect)
	bk.Post("/system/backups/{stamp}/restore", h.backupRestore)
	bk.Post("/system/backups/{stamp}/discard-partial", h.backupDiscardPartial)
	bk.With(auth.RequireRecentAuth).Get("/system/backups/{name}", h.backupDownload)

	keys := r.With(auth.RequirePerm(PermAPIKeys))
	keys.Get("/agents/keys", h.keysList)
	keys.Post("/agents/keys", h.keyCreate)
	keys.Post("/agents/keys/{id}/revoke", h.keyRevoke)
	keys.Post("/agents/keys/{id}/rotate", h.keyRotate)
	keys.Post("/agents/keys/{id}/model", h.agentModelSave)
}
