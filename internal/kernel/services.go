package kernel

// Names under which modules publish their services in Deps.Services
// (Services.Set in Register; Get[T](d, name) to consume).
const (
	ServiceAuth           = "auth"            // auth.Service
	ServiceAPIKeys        = "auth.keys"       // auth.KeyService
	ServiceAdminTemplates = "admin.templates" // *httpx.Templates (admin renderer)

	ServiceContent   = "content"   // content.Service
	ServiceTaxonomy  = "taxonomy"  // taxonomy.Service
	ServiceMedia     = "media"     // media.Service
	ServiceMenus     = "menus"     // menus.Service
	ServiceRedirects = "redirects" // redirects.Service
	ServiceSEO       = "seo"       // seo.Service
	ServiceTheme     = "theme"     // theme.Service
	ServiceCommerce  = "commerce"  // commerce service (wave 2)
	ServiceWebhooks  = "webhooks"  // webhooks.Service

	ServiceSchema = "kernel.schema" // kernel.SchemaInspector (set by the kernel after every module migrated)
)
